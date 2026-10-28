package social

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	// 连续几次检查都满足才出草稿 / 才算恢复，避免一次抖动就报
	outageConfirmChecks = 2
	// 一个模型层的最新探测超过 max(3 个间隔, 15 分钟) 视为不新鲜，不参与判断
	freshIntervals = 3
	freshMinWindow = 15 * time.Minute
	// 告警时全红的服务商连续这么久不在样本里，就当它已离开。移出热板、停用之后它不会
	// 再出现，一直等下去这个方向就卡在告警态：既出不了恢复草稿，新的异常也不会再报
	memberGoneAfter = 2 * time.Hour
)

// ServiceSnapshot 某服务方向在一次检查时的状况
type ServiceSnapshot struct {
	Service   string
	Providers int      // 有新鲜探测数据的服务商数
	Down      []string // 新鲜模型层全部为红的服务商（展示名，已排序）
	Up        []string // 有新鲜数据且没挂的服务商（展示名）
}

// Snapshot 把热板通道按服务方向归并。
//
// 一家服务商在某方向的**每个**新鲜模型层都是红的才算这家挂了。不能用组级 current_status：
// 主站取的是各层最差值，一条通道里只要一个模型红整条就是红。
// 没有任何新鲜模型层的服务商不计入分子也不计入分母——数据停更不等于挂了，也不等于恢复。
func Snapshot(groups []StatusGroup, now time.Time) map[string]*ServiceSnapshot {
	type providerState struct {
		name  string
		fresh int
		red   int
	}
	byService := map[string]map[string]*providerState{}
	for _, g := range groups {
		window := freshMinWindow
		if iv := time.Duration(g.IntervalMS) * time.Millisecond * freshIntervals; iv > window {
			window = iv
		}
		ps := byService[g.Service]
		if ps == nil {
			ps = map[string]*providerState{}
			byService[g.Service] = ps
		}
		st := ps[g.Provider]
		if st == nil {
			name := g.ProviderName
			if name == "" {
				name = g.Provider
			}
			st = &providerState{name: name}
			ps[g.Provider] = st
		}
		for _, layer := range g.Layers {
			ts := layer.CurrentStatus.Timestamp
			if ts <= 0 || now.Sub(time.Unix(ts, 0)) > window {
				continue
			}
			st.fresh++
			if layer.CurrentStatus.Status == statusRed {
				st.red++
			}
		}
	}
	out := map[string]*ServiceSnapshot{}
	for service, ps := range byService {
		snap := &ServiceSnapshot{Service: service}
		for _, st := range ps {
			if st.fresh == 0 {
				continue
			}
			snap.Providers++
			if st.red == st.fresh {
				snap.Down = append(snap.Down, st.name)
			} else {
				snap.Up = append(snap.Up, st.name)
			}
		}
		sort.Strings(snap.Down)
		out[service] = snap
	}
	return out
}

// Draft 一份草稿：Note 只给站长看，Text 是可直接转发的正文（为空＝只有说明）
type Draft struct {
	Note string
	Text string
}

type outageState struct {
	streak     int       // 连续满足异常条件的检查次数
	recoverRun int       // 告警后连续回落的检查次数
	alerted    bool      // 已出过异常草稿、还没出恢复草稿
	alertNeed  int       // 告警时的门槛；恢复按它判断，不随样本变化浮动
	startedAt  time.Time // 第一次满足条件的检查时刻
	lastAt     time.Time // 上一次样本足够、可用于判断的观测时刻
	peak       int       // 本次异常期间的最多全红家数

	// 告警时全红的服务商 → 从哪次观测起不在样本里（零值＝上次还在）。
	// 恢复须见到其中至少一半（按告警时的家数 memberTotal 算）重新变绿，退出样本不算；
	// 连续 memberGoneAfter 不在的移出名单，剩下的全变绿也凑不够一半时结束跟踪
	members     map[string]time.Time
	memberTotal int
	prunedAt    time.Time // 上一次刷新成员在场记录的观测时刻（含样本不足的观测）
}

// OutageDetector 按服务方向判断「多家同时异常」及其回落。非并发安全，由单个循环调用。
type OutageDetector struct {
	minProviders int
	ratio        float64
	maxGap       time.Duration // 两次有效观测间隔超过它，连续计数作废
	siteURL      string
	states       map[string]*outageState
}

// NewOutageDetector 创建检测器；maxGap 一般取两个检查间隔多一点
func NewOutageDetector(minProviders int, ratio float64, maxGap time.Duration, siteURL string) *OutageDetector {
	return &OutageDetector{minProviders: minProviders, ratio: ratio, maxGap: maxGap, siteURL: siteURL, states: map[string]*outageState{}}
}

// threshold 该方向判定为多家异常所需的全红家数；服务商不够多的方向返回 0 表示不判
func (d *OutageDetector) threshold(providers int) int {
	if providers < d.minProviders {
		return 0
	}
	n := int(math.Ceil(d.ratio * float64(providers)))
	if n < d.minProviders {
		n = d.minProviders
	}
	return n
}

// Observe 喂入一次有效检查结果，返回需要私发给站长的草稿。
// 本轮没出现的方向视为无数据，状态原样保留；已告警的方向例外，按全部服务商都不在样本里处理。
func (d *OutageDetector) Observe(snaps map[string]*ServiceSnapshot, now time.Time) []Draft {
	var drafts []Draft
	for _, service := range d.servicesToVisit(snaps) {
		snap := snaps[service]
		if snap == nil {
			snap = &ServiceSnapshot{Service: service}
		}
		st := d.states[service]
		if st == nil {
			st = &outageState{}
			d.states[service] = st
		}
		// 先于样本量判断：服务商都移出热板时，样本往往也一起不够了
		if st.alerted && !d.pruneDepartedMembers(st, snap, now) {
			drafts = append(drafts, d.untrackedDraft(snap, st, now))
			*st = outageState{lastAt: st.lastAt}
			continue
		}
		need := d.threshold(snap.Providers)
		down := len(snap.Down)
		if need == 0 {
			// 样本不足说明不了任何事：不算异常、不算恢复，也不刷新观测时刻
			if !st.alerted {
				st.streak = 0
			}
			continue
		}
		gap := !st.lastAt.IsZero() && now.Sub(st.lastAt) > d.maxGap
		st.lastAt = now

		if !st.alerted {
			if gap {
				st.streak = 0
			}
			if down < need {
				st.streak = 0
				continue
			}
			if st.streak == 0 {
				st.startedAt, st.peak = now, 0
			}
			st.streak++
			st.peak = max(st.peak, down)
			if st.streak >= outageConfirmChecks {
				st.alerted, st.alertNeed, st.recoverRun = true, need, 0
				st.members, st.memberTotal, st.prunedAt = map[string]time.Time{}, len(snap.Down), now
				for _, name := range snap.Down {
					st.members[name] = time.Time{}
				}
				drafts = append(drafts, d.outageDraft(snap, snaps, st))
			}
			continue
		}

		// 已告警：家数回落到告警门槛一半以下，且原先全红的服务商里至少一半重新出现且没挂
		if gap {
			st.recoverRun = 0
		}
		st.peak = max(st.peak, down)
		back := 0
		for _, name := range snap.Up {
			if _, ok := st.members[name]; ok {
				back++
			}
		}
		if down*2 < st.alertNeed && back*2 >= st.memberTotal {
			st.recoverRun++
			if st.recoverRun >= outageConfirmChecks {
				drafts = append(drafts, d.recoveryDraft(snap, st, now))
				*st = outageState{lastAt: now}
			}
		} else {
			st.recoverRun = 0
		}
	}
	return drafts
}

func (d *OutageDetector) outageDraft(snap *ServiceSnapshot, all map[string]*ServiceSnapshot, st *outageState) Draft {
	label := serviceLabel(snap.Service)
	text := fmt.Sprintf("%s 这边现在有点不对：我重点监测的 %d 家中转里，有 %d 家同时检测失败，已经持续一阵了。\n"+
		"原因还不确定，可能是上游波动，也可能是我这边探测的问题，确认后再更新。\n\n实时状态：%s",
		label, snap.Providers, len(snap.Down), d.siteURL)

	var others []string
	for _, s := range sortedKeys(all) {
		if s == snap.Service {
			continue
		}
		o := all[s]
		others = append(others, fmt.Sprintf("%s %d/%d", serviceLabel(s), len(o.Down), o.Providers))
	}
	note := fmt.Sprintf("异常草稿 · %s（%s 首次满足条件）\n"+
		"判断依据：热板里所有新鲜模型层都是红的服务商 %d/%d 家，门槛 %d 家，已连续 %d 次检查满足。\n"+
		"同期其他方向：%s。如果几个方向一起红，更可能是本站探测出了问题，先别发。\n"+
		"全红名单：%s",
		label, st.startedAt.In(cst).Format("15:04"),
		len(snap.Down), snap.Providers, st.alertNeed, outageConfirmChecks,
		orNone(strings.Join(others, "，")), strings.Join(snap.Down, "、"))
	return Draft{Note: note, Text: text}
}

func (d *OutageDetector) recoveryDraft(snap *ServiceSnapshot, st *outageState, now time.Time) Draft {
	label := serviceLabel(snap.Service)
	text := fmt.Sprintf("更新：%s 这边在好转，检测失败的已经回落到 %d 家。"+
		"我这边从 %s 起观测到这次异常，最多时 %d 家中转同时受影响。",
		label, len(snap.Down), st.startedAt.In(cst).Format("15:04"), st.peak)
	note := fmt.Sprintf("恢复草稿 · %s（%s 确认回落；对应之前那条异常草稿，那条没发的话这条也不用发）",
		label, now.In(cst).Format("15:04"))
	return Draft{Note: note, Text: text}
}

// untrackedDraft 只给站长的说明：离开样本的成员太多，判断不了这次异常是否过去，不出可转发的正文
func (d *OutageDetector) untrackedDraft(snap *ServiceSnapshot, st *outageState, now time.Time) Draft {
	note := fmt.Sprintf("异常跟踪结束 · %s（%s）：告警时全红的 %d 家里，有 %d 家已连续 %.0f 小时以上"+
		"不在热板数据里（移出热板或停止探测），剩下的即使全部恢复也不到一半，判断不了这次异常是否过去，"+
		"不再跟踪，也不会出恢复草稿。之前那条异常草稿如果发出去了，需要的话自己补一句说明。",
		serviceLabel(snap.Service), now.In(cst).Format("15:04"),
		st.memberTotal, st.memberTotal-len(st.members), memberGoneAfter.Hours())
	return Draft{Note: note}
}

// servicesToVisit 本轮要看的方向：出现在快照里的，加上已告警但本轮整个缺席的
func (d *OutageDetector) servicesToVisit(snaps map[string]*ServiceSnapshot) []string {
	services := sortedKeys(snaps)
	for service, st := range d.states {
		if _, ok := snaps[service]; !ok && st.alerted {
			services = append(services, service)
		}
	}
	sort.Strings(services)
	return services
}

// pruneDepartedMembers 刷新告警成员的在场记录，移出连续 memberGoneAfter 不在样本里的；
// 返回剩下的成员是否还可能凑够告警时家数的一半（凑不够就再也确认不了恢复）
func (d *OutageDetector) pruneDepartedMembers(st *outageState, snap *ServiceSnapshot, now time.Time) bool {
	// 与上次观测之间拉取断档：断档期间看不到它们，缺席从本次重新计时
	gap := now.Sub(st.prunedAt) > d.maxGap
	st.prunedAt = now
	present := make(map[string]bool, len(snap.Down)+len(snap.Up))
	for _, name := range snap.Down {
		present[name] = true
	}
	for _, name := range snap.Up {
		present[name] = true
	}
	for name, absentSince := range st.members {
		switch {
		case present[name]:
			st.members[name] = time.Time{}
		case absentSince.IsZero() || gap:
			st.members[name] = now
		case now.Sub(absentSince) >= memberGoneAfter:
			delete(st.members, name)
		}
	}
	return len(st.members)*2 >= st.memberTotal
}

// cst 文案里的时间一律按北京时间
var cst = time.FixedZone("CST", 8*3600)

func sortedKeys(m map[string]*ServiceSnapshot) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func orNone(s string) string {
	if s == "" {
		return "无"
	}
	return s
}
