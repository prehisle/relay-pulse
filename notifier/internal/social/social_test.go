package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"notifier/internal/config"
)

var t0 = time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)

// layer 一个模型层，status 为当前状态，age 为最新探测距 now 多久
func layer(status int, now time.Time, age time.Duration) StatusLayer {
	var l StatusLayer
	l.CurrentStatus.Status = status
	l.CurrentStatus.Timestamp = now.Add(-age).Unix()
	return l
}

func group(provider, service string, now time.Time, statuses ...int) StatusGroup {
	g := StatusGroup{Provider: provider, ProviderName: provider, Service: service, Channel: "main", IntervalMS: 300000}
	for _, st := range statuses {
		g.Layers = append(g.Layers, layer(st, now, time.Minute))
	}
	return g
}

// fleet n 家 service 方向的服务商，前 down 家唯一的模型层为红
func fleet(service string, n, down int, now time.Time) []StatusGroup {
	var gs []StatusGroup
	for i := 0; i < n; i++ {
		st := 1
		if i < down {
			st = statusRed
		}
		gs = append(gs, group(fmt.Sprintf("%s%02d", service, i), service, now, st))
	}
	return gs
}

func TestSnapshot_ProviderDownOnlyWhenEveryFreshLayerRed(t *testing.T) {
	groups := []StatusGroup{
		group("mixed", "cx", t0, statusRed, 1), // 一个模型红、一个绿：组级状态是红，但这家没挂
		group("twoch", "cx", t0, statusRed),
		group("twoch", "cx", t0, statusRed, statusRed),
		group("halfch", "cx", t0, statusRed),
		group("halfch", "cx", t0, 1),
	}
	stale := group("stale", "cx", t0)
	stale.Layers = []StatusLayer{layer(statusRed, t0, 2*time.Hour)} // 数据停更，不计入
	partly := group("partly", "cx", t0)
	partly.Layers = []StatusLayer{layer(1, t0, 2*time.Hour), layer(statusRed, t0, time.Minute)}
	groups = append(groups, stale, partly)

	snap := Snapshot(groups, t0)["cx"]
	if snap.Providers != 4 {
		t.Fatalf("应只计有新鲜数据的 4 家，得到 %d", snap.Providers)
	}
	if strings.Join(snap.Down, ",") != "partly,twoch" {
		t.Fatalf("全红名单不对: %v", snap.Down)
	}
}

func TestSnapshot_FreshWindowFollowsInterval(t *testing.T) {
	g := group("slow", "cc", t0)
	g.IntervalMS = int64(time.Hour / time.Millisecond) // 一小时一探，40 分钟前的结果仍新鲜
	g.Layers = []StatusLayer{layer(statusRed, t0, 40*time.Minute)}
	if snap := Snapshot([]StatusGroup{g}, t0)["cc"]; snap.Providers != 1 || len(snap.Down) != 1 {
		t.Fatalf("长间隔通道的新鲜窗口应放宽: %+v", snap)
	}
}

func newDetector() *OutageDetector {
	return NewOutageDetector(5, 0.5, 11*time.Minute, "https://example.test")
}

func observe(d *OutageDetector, n, down int, at time.Time) []Draft {
	return d.Observe(Snapshot(fleet("cx", n, down, at), at), at)
}

func TestOutageDetector_NeedsConsecutiveChecksThenRecovers(t *testing.T) {
	d := newDetector()
	step := func(i int) time.Time { return t0.Add(time.Duration(i) * 5 * time.Minute) }

	if got := observe(d, 20, 10, step(0)); len(got) != 0 {
		t.Fatalf("第一次满足不应出草稿，得到 %d 份", len(got))
	}
	got := observe(d, 20, 12, step(1))
	if len(got) != 1 || !strings.Contains(got[0].Text, "20 家中转里，有 12 家同时检测失败") {
		t.Fatalf("连续两次满足应出异常草稿: %+v", got)
	}
	if !strings.Contains(got[0].Text, "原因还不确定") || !strings.Contains(got[0].Note, "门槛 10 家") {
		t.Fatalf("异常草稿内容不对: %+v", got[0])
	}
	if got := observe(d, 20, 15, step(2)); len(got) != 0 {
		t.Fatalf("告警期间不应重复出草稿: %+v", got)
	}
	// 恰好等于门槛一半不算回落
	if got := observe(d, 20, 5, step(3)); len(got) != 0 {
		t.Fatalf("等于一半不算回落: %+v", got)
	}
	observe(d, 20, 4, step(4))
	if got := observe(d, 20, 6, step(5)); len(got) != 0 { // 回落被打断
		t.Fatalf("回落被打断不应恢复: %+v", got)
	}
	observe(d, 20, 4, step(6))
	got = observe(d, 20, 3, step(7))
	if len(got) != 1 || !strings.Contains(got[0].Text, "最多时 15 家") || !strings.Contains(got[0].Text, "回落到 3 家") ||
		!strings.Contains(got[0].Text, "从 14:00 起观测到") || strings.Contains(got[0].Text, "持续") {
		t.Fatalf("应出恢复草稿: %+v", got)
	}
	if got := observe(d, 20, 10, step(8)); len(got) != 0 {
		t.Fatalf("恢复后应重新计数: %+v", got)
	}
}

func TestOutageDetector_FlapResetsStreak(t *testing.T) {
	d := newDetector()
	observe(d, 20, 10, t0)
	observe(d, 20, 2, t0.Add(5*time.Minute))
	if got := observe(d, 20, 10, t0.Add(10*time.Minute)); len(got) != 0 {
		t.Fatalf("中间断过一次，不应出草稿: %+v", got)
	}
}

func TestOutageDetector_ObservationGapResetsStreak(t *testing.T) {
	d := newDetector()
	observe(d, 20, 10, t0)
	// 中间几小时拉不到数据，两次异常不能连成「持续」
	if got := observe(d, 20, 10, t0.Add(3*time.Hour)); len(got) != 0 {
		t.Fatalf("观测断档后不应直接告警: %+v", got)
	}
	if got := observe(d, 20, 10, t0.Add(3*time.Hour+5*time.Minute)); len(got) != 1 {
		t.Fatalf("断档后重新连续两次应告警: %+v", got)
	}
}

func TestOutageDetector_ShrinkingSampleIsNotRecovery(t *testing.T) {
	d := newDetector()
	observe(d, 5, 5, t0)
	if got := observe(d, 5, 5, t0.Add(5*time.Minute)); len(got) != 1 {
		t.Fatalf("5/5 应告警: %+v", got)
	}
	// 一家移出热板，剩 4/4 全红：样本不足，不能报恢复
	for i := 2; i < 6; i++ {
		if got := observe(d, 4, 4, t0.Add(time.Duration(i)*5*time.Minute)); len(got) != 0 {
			t.Fatalf("样本不足不应报恢复: %+v", got)
		}
	}
	// 大半服务商退出样本、剩下的 2/4 红：数量上「回落」了，但样本不足，同样不算恢复
	for i := 6; i < 10; i++ {
		if got := observe(d, 4, 2, t0.Add(time.Duration(i)*5*time.Minute)); len(got) != 0 {
			t.Fatalf("样本不足时的回落不应报恢复: %+v", got)
		}
	}
}

func TestOutageDetector_RecoveryUsesThresholdFrozenAtAlert(t *testing.T) {
	d := newDetector()
	observe(d, 20, 10, t0)
	observe(d, 20, 10, t0.Add(5*time.Minute)) // 告警，门槛冻结为 10
	// 样本扩到 40 家：原先 10 家全部变绿，另有 6 家新挂。
	// 按冻结门槛 6×2 ≥ 10 还不算回落；若按新样本的动态门槛 20 就会被误判为恢复
	for i := 2; i < 6; i++ {
		at := t0.Add(time.Duration(i) * 5 * time.Minute)
		gs := fleet("cx", 40, 0, at)
		for j := 10; j < 16; j++ {
			gs[j].Layers[0].CurrentStatus.Status = statusRed
		}
		if got := d.Observe(Snapshot(gs, at), at); len(got) != 0 {
			t.Fatalf("应按告警时的门槛判断回落: %+v", got)
		}
	}
}

func TestOutageDetector_SmallServiceNeverAlerts(t *testing.T) {
	d := newDetector()
	for i := 0; i < 4; i++ {
		at := t0.Add(time.Duration(i) * 5 * time.Minute)
		if got := d.Observe(Snapshot(fleet("gm", 3, 3, at), at), at); len(got) != 0 {
			t.Fatalf("不足 5 家的方向全红也不判: %+v", got)
		}
	}
}

func TestOutageDetector_MembersLeavingSampleIsNotRecovery(t *testing.T) {
	d := newDetector()
	observe(d, 20, 10, t0)
	if got := observe(d, 20, 10, t0.Add(5*time.Minute)); len(got) != 1 {
		t.Fatalf("10/20 应告警: %+v", got)
	}
	// 原先全红的 10 家（cx00-cx09）数据全部停更，只剩 10 家健康的：没有任何一家「恢复」的证据
	for i := 2; i < 6; i++ {
		at := t0.Add(time.Duration(i) * 5 * time.Minute)
		healthyOnly := fleet("cx", 20, 0, at)[10:]
		if got := d.Observe(Snapshot(healthyOnly, at), at); len(got) != 0 {
			t.Fatalf("故障成员退出样本不应报恢复: %+v", got)
		}
	}
	// 原先的成员重新出现且变绿，才算恢复
	observe(d, 20, 0, t0.Add(30*time.Minute))
	if got := observe(d, 20, 0, t0.Add(35*time.Minute)); len(got) != 1 {
		t.Fatalf("成员变绿后应报恢复: %+v", got)
	}
}

func TestOutageDetector_InsufficientSampleDoesNotBridgeGap(t *testing.T) {
	d := newDetector()
	observe(d, 10, 5, t0)
	observe(d, 10, 5, t0.Add(5*time.Minute))  // 告警：cx00-cx04 全红，门槛 5
	observe(d, 10, 0, t0.Add(10*time.Minute)) // 回落一次
	// 之后几个小时只剩 4 家有数据（恰是原先挂的那几家，且已变绿）：样本不足，
	// 既不能算作回落确认，也不能刷新观测时刻、把断档前后的两次回落连起来
	for i := 3; i < 40; i++ {
		at := t0.Add(time.Duration(i) * 5 * time.Minute)
		if got := d.Observe(Snapshot(fleet("cx", 4, 0, at), at), at); len(got) != 0 {
			t.Fatalf("样本不足时不应报恢复: %+v", got)
		}
	}
	if got := observe(d, 10, 0, t0.Add(200*time.Minute)); len(got) != 0 {
		t.Fatalf("断档前后的回落不应连成恢复: %+v", got)
	}
	if got := observe(d, 10, 0, t0.Add(205*time.Minute)); len(got) != 1 {
		t.Fatalf("断档后重新连续两次回落应报恢复: %+v", got)
	}
}

func TestOutageDetector_Threshold(t *testing.T) {
	d := newDetector()
	cases := map[int]int{3: 0, 4: 0, 5: 5, 9: 5, 11: 6, 17: 9, 39: 20}
	for providers, want := range cases {
		if got := d.threshold(providers); got != want {
			t.Errorf("threshold(%d)=%d，期望 %d", providers, got, want)
		}
	}
}

// day 第 i 天的桶起点（UTC 零点），与主站 7d 视图一致
func day(i int) int64 { return time.Date(2026, 10, 17+i, 0, 0, 0, 0, time.UTC).Unix() }

func bucket(i int, avail float64, probes, latency int) StatusBucket {
	b := StatusBucket{Time: time.Unix(day(i), 0).UTC().Format("2006-01-02"), Timestamp: day(i), Availability: avail, Latency: latency}
	b.StatusCounts.Available = probes
	return b
}

// steadyLayer 7 个桶，每天 288 次、可用率恒定
func steadyLayer(avail float64) StatusLayer {
	var l StatusLayer
	for i := 0; i < 7; i++ {
		l.Timeline = append(l.Timeline, bucket(i, avail, 288, 2000))
	}
	return l
}

func weekGroup(provider, service string, listedDays int, layers ...StatusLayer) StatusGroup {
	return StatusGroup{Provider: provider, ProviderName: provider, Service: service, ChannelName: "O-Max",
		ListedDays: listedDays, IntervalMS: 300000, Layers: layers}
}

// weekNow 第 7 个桶当天结束时，窗口正好 7 天
var weekNow = time.Unix(day(7), 0)

func TestBuildWeeklyReport_WeightsByProbesAcrossBucketsAndLayers(t *testing.T) {
	// 不等量样本：桶等权或层等权都会得出别的数，用来锁死「按探测次数加权」口径
	uneven := StatusLayer{Timeline: []StatusBucket{
		bucket(0, 100, 900, 1000),
		bucket(1, 50, 100, 1000),
		{Time: "2026-10-19", Timestamp: day(2), Availability: -1}, // 无数据桶不计入
	}}
	second := StatusLayer{Timeline: []StatusBucket{bucket(0, 0, 500, 1000)}}
	cw := summarizeChannel(weekGroup("p", "cc", 30, uneven, second), weekNow)
	// (100×900 + 50×100 + 0×500) / 1500 = 63.33；桶等权 = 50，层等权 = (95+0)/2 = 47.5
	if cw.probes != 1500 || math.Abs(cw.availability-95000.0/1500) > 1e-9 {
		t.Fatalf("加权口径不对: probes=%d availability=%v", cw.probes, cw.availability)
	}

	// 黄色与缺失计入分母：可用率由主站按权重算好，这里只按总探测次数加权
	mixed := bucket(0, 85, 0, 1000)
	mixed.StatusCounts.Available, mixed.StatusCounts.Degraded, mixed.StatusCounts.Missing = 70, 20, 10
	other := bucket(1, 100, 300, 1000)
	cw = summarizeChannel(weekGroup("q", "cc", 30, StatusLayer{Timeline: []StatusBucket{mixed, other}}), weekNow)
	// (85×100 + 100×300) / 400 = 96.25
	if cw.probes != 400 || math.Abs(cw.availability-96.25) > 1e-9 {
		t.Fatalf("黄色/缺失口径不对: probes=%d availability=%v", cw.probes, cw.availability)
	}

	r, err := BuildWeeklyReport([]StatusGroup{
		weekGroup("a", "cc", 30, steadyLayer(99)),
		weekGroup("b", "cx", 30, StatusLayer{Timeline: []StatusBucket{bucket(0, 80, 2016, 1000)}}),
	}, -1, "https://example.test", weekNow)
	if err != nil {
		t.Fatal(err)
	}
	// a: 99×2016，b: 80×2016 → 89.5
	if math.Abs(r.Overall-89.5) > 1e-9 {
		t.Fatalf("整体可用率=%v，期望 89.5", r.Overall)
	}
	if strings.Contains(r.Text, "比上周") {
		t.Fatalf("没有上周数据不应写环比:\n%s", r.Text)
	}
	// 上榜即点名：低于 90% 的不能因为凑不满前五就被列进去
	if !strings.Contains(r.Text, "1. a · O-Max") || strings.Contains(r.Text, "b · O-Max") {
		t.Fatalf("低于 90%% 的通道不应上榜:\n%s", r.Text)
	}
}

func TestBuildWeeklyReport_Ranking(t *testing.T) {
	sparse := StatusLayer{}
	for i := 0; i < 7; i++ {
		sparse.Timeline = append(sparse.Timeline, bucket(i, 100, 100, 2000)) // 只有应有的 1/3
	}
	groups := []StatusGroup{
		weekGroup("fresh", "cc", 2, steadyLayer(100)), // 收录不满 7 天
		weekGroup("sparse", "cc", 30, sparse),         // 样本太少
		weekGroup("bad", "cx", 30, steadyLayer(70)),   // 低于 90%：不点名
		weekGroup("dup", "cx", 30, steadyLayer(99.8)),
		weekGroup("dup", "gm", 30, steadyLayer(99.9)), // 同一家只取最好的一条
	}
	for i, avail := range []float64{99.7, 99.6, 99.5, 99.4, 99.3} {
		groups = append(groups, weekGroup(fmt.Sprintf("ok%d", i), "cx", 30, steadyLayer(avail)))
	}
	r, err := BuildWeeklyReport(groups, 90, "https://example.test", weekNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1. dup · O-Max（Gemini）99.9%",
		"2. ok0 · O-Max（Codex）99.7%",
		"5. ok3 · O-Max（Codex）99.4%",
		"另有 1 条通道这周低于 90%",
		"10/17–10/23",
		"比上周",
	} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("缺少 %q:\n%s", want, r.Text)
		}
	}
	for _, bad := range []string{"fresh", "sparse", "bad ·", "ok4", "6. ", "dup · O-Max（Codex）"} {
		if strings.Contains(r.Text, bad) {
			t.Fatalf("不应出现 %q:\n%s", bad, r.Text)
		}
	}
}

func TestBuildWeeklyReport_CoverageUsesActualWindow(t *testing.T) {
	// 周一中午（UTC）：窗口 = 6 个完整 UTC 日 + 半天 = 6.5 天，5 分钟间隔应有 1872 次，70% 门槛 = 1310.4
	now := time.Unix(day(6), 0).Add(12 * time.Hour)
	withProbes := func(total int) StatusGroup {
		l := StatusLayer{}
		per := total / 7
		for i := 0; i < 7; i++ {
			n := per
			if i == 6 {
				n = total - per*6
			}
			l.Timeline = append(l.Timeline, bucket(i, 99, n, 1000))
		}
		return weekGroup(fmt.Sprintf("p%d", total), "cx", 30, l)
	}
	if !summarizeChannel(withProbes(1311), now).eligible {
		t.Fatal("1311 次刚过 70%，应上榜")
	}
	if summarizeChannel(withProbes(1310), now).eligible {
		t.Fatal("1310 次不到 70%，不应上榜")
	}
	// 空模型层同样计入应有次数：一层满、一层全无数据的通道只有一半覆盖
	full := withProbes(1872)
	empty := StatusLayer{Timeline: []StatusBucket{{Time: "2026-10-17", Timestamp: day(0), Availability: -1}}}
	full.Layers = append(full.Layers, empty)
	if summarizeChannel(full, now).eligible {
		t.Fatal("一半模型没有数据的通道不应上榜")
	}
}

func TestFormatPct_NeverRoundsUpTo100(t *testing.T) {
	if got := formatPct(99.97); got != "99.9%" {
		t.Fatalf("formatPct(99.97)=%s", got)
	}
	if got := formatPct(100); got != "100%" {
		t.Fatalf("formatPct(100)=%s", got)
	}
}

type memStore struct{ kv map[string]string }

func (m *memStore) GetSocialState(_ context.Context, k string) (string, bool, error) {
	v, ok := m.kv[k]
	return v, ok, nil
}

func (m *memStore) SetSocialState(_ context.Context, k, v string) error {
	m.kv[k] = v
	return nil
}

type sent struct {
	chatID int64
	text   string
}

type recorder struct {
	sent []sent
	fail bool
}

func (r *recorder) SendText(_ context.Context, chatID int64, text string) error {
	if r.fail {
		return errors.New("telegram down")
	}
	r.sent = append(r.sent, sent{chatID, text})
	return nil
}

func statusServer(t *testing.T, handle func(period string) []StatusGroup) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" || r.URL.Query().Get("board") != "hot" {
			t.Errorf("请求不对: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"groups": handle(r.URL.Query().Get("period"))})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestService(siteURL string, store StateStore, out Messenger) *Service {
	cfg := config.SocialConfig{AdminChatID: 42, SiteURL: siteURL, CheckInterval: 5 * time.Minute, OutageMinProviders: 5, OutageRatio: 0.5}
	return NewService(cfg, store, out)
}

func TestMaybeWeekly_OncePerWeekOnMondayOnly(t *testing.T) {
	avail := 99.5
	srv := statusServer(t, func(period string) []StatusGroup {
		if period != "7d" {
			t.Errorf("周报应取 7d，得到 %s", period)
		}
		return []StatusGroup{weekGroup("good", "cx", 30, steadyLayer(avail))}
	})
	store := &memStore{kv: map[string]string{}}
	out := &recorder{}
	svc := newTestService(srv.URL, store, out)
	at := func(ts time.Time) { svc.now = func() time.Time { return ts } }
	ctx := context.Background()

	at(time.Date(2026, 10, 19, 9, 59, 0, 0, cst)) // 周一 10 点前
	if err := svc.maybeWeekly(ctx); err != nil || len(out.sent) != 0 {
		t.Fatalf("10 点前不应发: err=%v sent=%d", err, len(out.sent))
	}
	at(time.Date(2026, 10, 19, 10, 5, 0, 0, cst))
	if err := svc.maybeWeekly(ctx); err != nil || len(out.sent) != 2 {
		t.Fatalf("周一 10 点后应发说明+正文两条: err=%v sent=%d", err, len(out.sent))
	}
	if out.sent[0].chatID != 42 || out.sent[1].chatID != 42 {
		t.Fatalf("应只发给站长: %+v", out.sent)
	}
	if store.kv[stateWeeklyLast] != "2026-W43 99.5000" {
		t.Fatalf("应记下本周与整体可用率: %q", store.kv[stateWeeklyLast])
	}
	at(time.Date(2026, 10, 19, 15, 0, 0, 0, cst))
	if err := svc.maybeWeekly(ctx); err != nil || len(out.sent) != 2 {
		t.Fatalf("同一周不应重复发: sent=%d", len(out.sent))
	}
	at(time.Date(2026, 10, 21, 10, 0, 0, 0, cst))
	if err := svc.maybeWeekly(ctx); err != nil || len(out.sent) != 2 {
		t.Fatalf("不是周一不发: sent=%d", len(out.sent))
	}

	// 手动预览：发消息、不带环比、不动定时进度
	if err := svc.PreviewWeekly(ctx); err != nil || len(out.sent) != 4 || strings.Contains(out.sent[3].text, "比上周") {
		t.Fatalf("预览不对: err=%v sent=%d", err, len(out.sent))
	}
	if store.kv[stateWeeklyLast] != "2026-W43 99.5000" {
		t.Fatalf("预览不应改进度: %q", store.kv[stateWeeklyLast])
	}

	avail = 98.5
	at(time.Date(2026, 10, 26, 10, 0, 0, 0, cst))
	if err := svc.maybeWeekly(ctx); err != nil || len(out.sent) != 6 {
		t.Fatalf("下周一应再发: sent=%d", len(out.sent))
	}
	if !strings.Contains(out.sent[5].text, "比上周低 1.0 个百分点") {
		t.Fatalf("相邻周应带环比:\n%s", out.sent[5].text)
	}

	// 停了一周没发：两周后不能拿两周前的数当「上周」
	at(time.Date(2026, 11, 9, 10, 0, 0, 0, cst))
	if err := svc.maybeWeekly(ctx); err != nil || len(out.sent) != 8 {
		t.Fatalf("隔周应照常发: sent=%d", len(out.sent))
	}
	if strings.Contains(out.sent[7].text, "上周") {
		t.Fatalf("不相邻的周不应写环比:\n%s", out.sent[7].text)
	}
}

func TestCheckOutage_UndeliveredDraftsAreRetried(t *testing.T) {
	down := 8
	srv := statusServer(t, func(period string) []StatusGroup {
		if period != "90m" {
			t.Errorf("异常检查应取 90m，得到 %s", period)
		}
		now := time.Now()
		return append(fleet("cc", 10, down, now), fleet("cx", 10, down, now)...)
	})
	out := &recorder{}
	svc := newTestService(srv.URL, &memStore{kv: map[string]string{}}, out)
	ctx := context.Background()

	if err := svc.checkOutage(ctx); err != nil {
		t.Fatal(err)
	}
	out.fail = true
	if err := svc.checkOutage(ctx); err == nil || len(svc.pending) != 2 {
		t.Fatalf("两个方向的草稿都应留待重发: err=%v pending=%d", err, len(svc.pending))
	}
	out.fail = false
	if err := svc.checkOutage(ctx); err != nil || len(svc.pending) != 0 {
		t.Fatalf("恢复后应补发: err=%v pending=%d", err, len(svc.pending))
	}
	if len(out.sent) != 4 || !strings.Contains(out.sent[1].text, "Claude Code") || !strings.Contains(out.sent[3].text, "Codex") {
		t.Fatalf("补发内容不对: %+v", out.sent)
	}
}

func TestCheckOutage_StaleDraftsAreDropped(t *testing.T) {
	now := t0
	srv := statusServer(t, func(string) []StatusGroup { return fleet("cx", 10, 8, now) })
	out := &recorder{fail: true}
	svc := newTestService(srv.URL, &memStore{kv: map[string]string{}}, out)
	svc.now = func() time.Time { return now }
	ctx := context.Background()

	_ = svc.checkOutage(ctx)
	now = now.Add(5 * time.Minute)
	if err := svc.checkOutage(ctx); err == nil || len(svc.pending) != 1 {
		t.Fatalf("应积压 1 份: err=%v pending=%d", err, len(svc.pending))
	}
	// Telegram 三个小时后才恢复：「现在有点不对」已经过时，不应再发
	out.fail = false
	now = now.Add(3 * time.Hour)
	_ = svc.checkOutage(ctx)
	for _, m := range out.sent {
		if strings.Contains(m.text, "现在有点不对") {
			t.Fatalf("过时的异常草稿不应补发: %+v", out.sent)
		}
	}
	if len(svc.pending) != 0 {
		t.Fatalf("过时草稿应被丢弃: pending=%d", len(svc.pending))
	}
}

func TestIsoWeekKey(t *testing.T) {
	if got := isoWeekKey(time.Date(2026, 10, 19, 10, 0, 0, 0, cst)); got != "2026-W43" {
		t.Fatalf("isoWeekKey=%s", got)
	}
}
