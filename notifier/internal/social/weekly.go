package social

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	weeklyTopN = 5
	// 进前几名的门槛：收录满 7 天，且实际探测次数不少于按间隔推算的 70%，
	// 避免刚收录一天或长期没数据的 100% 排到前面
	weeklyMinListedDays   = 7
	weeklyMinCoverage     = 0.7
	weeklyLowAvailability = 90.0
)

// channelWeek 一个通道近 7 天的汇总
type channelWeek struct {
	group        StatusGroup
	availability float64 // 按探测次数加权，跨模型层合并
	latency      float64 // 按探测次数加权的平均延迟（ms）
	probes       int
	eligible     bool // 是否满足进前几名的门槛
}

// WeeklyReport 周报草稿
type WeeklyReport struct {
	Text    string  // 可直接转发的正文
	Note    string  // 只给站长看的口径说明
	Overall float64 // 整体可用率，下周用来比较
}

func summarizeChannel(g StatusGroup, now time.Time) channelWeek {
	var weighted, latencySum, expected float64
	var probes int
	for _, layer := range g.Layers {
		var start int64
		for _, b := range layer.Timeline {
			if b.Timestamp > 0 && (start == 0 || b.Timestamp < start) {
				start = b.Timestamp
			}
			n := b.probes()
			if n == 0 || b.Availability < 0 {
				continue
			}
			weighted += b.Availability * float64(n)
			latencySum += float64(b.Latency) * float64(n)
			probes += n
		}
		// 应有探测次数按窗口实际长度估算；group 只给出父行间隔，子层若自定义间隔会有偏差
		if start > 0 && g.IntervalMS > 0 {
			expected += now.Sub(time.Unix(start, 0)).Seconds() * 1000 / float64(g.IntervalMS)
		}
	}
	cw := channelWeek{group: g, probes: probes}
	if probes == 0 {
		return cw
	}
	cw.availability = weighted / float64(probes)
	cw.latency = latencySum / float64(probes)
	cw.eligible = g.ListedDays >= weeklyMinListedDays && expected > 0 && float64(probes) >= weeklyMinCoverage*expected
	return cw
}

// BuildWeeklyReport 由热板 7 天数据生成周报。prevOverall<0 表示没有上周数据。
func BuildWeeklyReport(groups []StatusGroup, prevOverall float64, siteURL string, now time.Time) (*WeeklyReport, error) {
	var weeks []channelWeek
	serviceCount := map[string]int{}
	var weighted float64
	var probes int
	var firstDay, lastDay string
	for _, g := range groups {
		cw := summarizeChannel(g, now)
		if cw.probes == 0 {
			continue
		}
		weeks = append(weeks, cw)
		serviceCount[g.Service]++
		weighted += cw.availability * float64(cw.probes)
		probes += cw.probes
		for _, layer := range g.Layers {
			for _, b := range layer.Timeline {
				if b.Time == "" {
					continue
				}
				if firstDay == "" || b.Time < firstDay {
					firstDay = b.Time
				}
				if b.Time > lastDay {
					lastDay = b.Time
				}
			}
		}
	}
	if probes == 0 {
		return nil, fmt.Errorf("热板 7 天内没有任何探测数据")
	}
	overall := weighted / float64(probes)

	// 前几名：每家服务商只取最好的一条通道
	var eligible []channelWeek
	for _, cw := range weeks {
		// 低于「另有 N 条」那条线的不上榜：上榜即点名，差的只报数量
		if cw.eligible && cw.availability >= weeklyLowAvailability {
			eligible = append(eligible, cw)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].availability != eligible[j].availability {
			return eligible[i].availability > eligible[j].availability
		}
		return eligible[i].latency < eligible[j].latency
	})
	var top []channelWeek
	seen := map[string]bool{}
	for _, cw := range eligible {
		if seen[cw.group.Provider] {
			continue
		}
		seen[cw.group.Provider] = true
		top = append(top, cw)
		if len(top) == weeklyTopN {
			break
		}
	}

	low := 0
	for _, cw := range weeks {
		if cw.availability < weeklyLowAvailability {
			low++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "RelayPulse 周报 · %s–%s\n\n", shortDay(firstDay), shortDay(lastDay))
	fmt.Fprintf(&sb, "目前热板上重点监测的 %d 条通道（%s），近 7 天按探测次数算的整体可用率 %.1f%%",
		len(weeks), serviceBreakdown(serviceCount), overall)
	if prevOverall >= 0 {
		diff := overall - prevOverall
		switch {
		case math.Abs(diff) < 0.05:
			fmt.Fprintf(&sb, "，和上周持平")
		case diff > 0:
			fmt.Fprintf(&sb, "，比上周高 %.1f 个百分点", diff)
		default:
			fmt.Fprintf(&sb, "，比上周低 %.1f 个百分点", -diff)
		}
	}
	sb.WriteString("。\n")
	if len(top) > 0 {
		sb.WriteString("\n可用率靠前的几家（只算收录满一周的通道）：\n")
		for i, cw := range top {
			fmt.Fprintf(&sb, "%d. %s（%s）%s\n", i+1, cw.group.displayName(), serviceLabel(cw.group.Service), formatPct(cw.availability))
		}
	}
	if low > 0 {
		fmt.Fprintf(&sb, "\n另有 %d 条通道这周低于 %.0f%%，各家明细在站上可以看。\n", low, weeklyLowAvailability)
	}
	fmt.Fprintf(&sb, "\n%s", siteURL)

	note := fmt.Sprintf("周报草稿（确认无误后转发下一条到群里，或复制到 X）\n"+
		"口径：当前热板的通道、近 7 个自然日（含今天未结束的部分），按探测次数加权、黄色按主站权重折算；"+
		"本周掉出热板的通道不在里面。前几名门槛：收录满 %d 天、实际探测不少于应有次数的 %.0f%%（按父行间隔估算）、可用率不低于 %.0f%%，同一家只取一条。"+
		"符合门槛 %d 条，共 %d 条。",
		weeklyMinListedDays, weeklyMinCoverage*100, weeklyLowAvailability, len(eligible), len(weeks))

	return &WeeklyReport{Text: sb.String(), Note: note, Overall: overall}, nil
}

// formatPct 可用率显示：没到 100 的不四舍五入成 100.0
func formatPct(v float64) string {
	if v >= 100 {
		return "100%"
	}
	return fmt.Sprintf("%.1f%%", math.Floor(v*10)/10)
}

func serviceBreakdown(count map[string]int) string {
	order := []string{"cc", "cx", "gm"}
	var parts []string
	used := map[string]bool{}
	for _, s := range order {
		if count[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", serviceLabel(s), count[s]))
			used[s] = true
		}
	}
	var rest []string
	for s := range count {
		if !used[s] {
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	for _, s := range rest {
		parts = append(parts, fmt.Sprintf("%s %d", serviceLabel(s), count[s]))
	}
	return strings.Join(parts, " / ")
}

// shortDay 2026-10-17 → 10/17
func shortDay(day string) string {
	if len(day) == len("2006-01-02") {
		return strings.TrimPrefix(day[5:7], "0") + "/" + strings.TrimPrefix(day[8:], "0")
	}
	return day
}
