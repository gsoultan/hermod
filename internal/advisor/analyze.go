// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Report states. A flow that is off has no engine to read.
const (
	StateNotRunning = "not_running"
	// StateCollecting: running, but the window is too short or too quiet for
	// the rules that compare readings over time. Rules that read one reading
	// or the flow's design still report.
	StateCollecting = "collecting"
	StateReady      = "ready"
)

// Severities, most urgent first.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// Rules.
const (
	RuleBottleneck       = "bottleneck"
	RuleFilterEarlier    = "filter_earlier"
	RuleSinkBackpressure = "sink_backpressure"
	RuleSinkBreaker      = "sink_breaker"
	RuleStepErrors       = "step_errors"
	RuleDeadLetters      = "dead_letters"
	RuleStuckStep        = "stuck_step"
	RuleSourceLag        = "source_lag"
	RuleLookupCacheOff   = "lookup_cache_off"
	RuleTraceSampling    = "trace_sampling"
)

// What a window needs before rules that compare readings over time speak.
const (
	minWindowSpan      = 2 * time.Minute
	minWindowSamples   = 5
	minWindowProcessed = 100
)

// Thresholds. Each is the point past which the reading is worth a person's
// attention, not a statistical bound.
const (
	// A step is the bottleneck when it takes this share of all step time.
	bottleneckShare     = 0.4
	bottleneckShareHigh = 0.7
	// Steps faster than this are not worth tuning, whatever their share.
	minWorthTuning = time.Millisecond
	// A sink is backed up when its buffer is this full in this share of
	// readings: a lasting state, not a burst.
	fullBuffer      = 0.8
	fullBufferShare = 0.5
	minFullReadings = 3
	// A filter worth moving drops at least this share.
	filterDropShare = 0.5
	// ...and runs after a step at least this many times slower than itself.
	filterCostRatio = 2
	stepErrorShare  = 0.05
	stepErrorHigh   = 0.2
	minStepErrors   = 5
	deadLetterHigh  = 0.05
	stuckAfter      = 60 * time.Second
	lagFloor        = 10_000
	lagGrowth       = 1.5
	lagRisingShare  = 0.7
	lagSinkCalm     = 0.5
	defaultBatch    = 100
)

// Evidence is one number a finding was drawn from.
type Evidence struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Finding is one recommendation.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	// NodeID is the step it is about, SinkID the sink connection.
	NodeID string `json:"node_id,omitempty"`
	SinkID string `json:"sink_id,omitempty"`
	// Title is the headline, Detail what was seen, Recommendation what to do.
	Title          string     `json:"title"`
	Detail         string     `json:"detail"`
	Recommendation string     `json:"recommendation"`
	Evidence       []Evidence `json:"evidence,omitempty"`

	// score orders findings of the same severity: bigger first.
	score float64
}

// Report is the advisor's reading of one flow.
type Report struct {
	WorkflowID  string    `json:"workflow_id"`
	State       string    `json:"state"`
	WindowStart time.Time `json:"window_start,omitzero"`
	WindowEnd   time.Time `json:"window_end,omitzero"`
	Samples     int       `json:"samples"`
	// Processed is messages in the window; Throughput their average rate.
	Processed        uint64    `json:"processed"`
	Throughput       float64   `json:"throughput"`
	BottleneckNodeID string    `json:"bottleneck_node_id,omitempty"`
	Findings         []Finding `json:"findings"`
}

// window is the samples since the engine last started, with the deltas the
// rules read.
type window struct {
	s     []Sample
	first Sample
	last  Sample
	span  time.Duration
}

func (w window) ready() bool {
	return len(w.s) >= minWindowSamples && w.span >= minWindowSpan &&
		w.last.Processed-w.first.Processed >= minWindowProcessed
}

func (w window) runs(id string) uint64   { return w.last.NodeRuns[id] - w.first.NodeRuns[id] }
func (w window) errors(id string) uint64 { return w.last.NodeErrors[id] - w.first.NodeErrors[id] }

// latency is a step's average run time over the window: each reading's
// moving average weighted by the runs since the reading before it.
func (w window) latency(id string) time.Duration {
	var total float64
	var runs uint64
	for i := 1; i < len(w.s); i++ {
		d := w.s[i].NodeRuns[id] - w.s[i-1].NodeRuns[id]
		total += float64(w.s[i].NodeLatency[id]) * float64(d)
		runs += d
	}
	if runs == 0 {
		return w.last.NodeLatency[id]
	}
	return time.Duration(total / float64(runs))
}

// edge returns an edge's messages over the window, and false when the frame
// does not count it.
func (w window) edge(id string) (uint64, bool) {
	if id == "" {
		return 0, false
	}
	last, ok := w.last.EdgeCounts[id]
	if !ok {
		return 0, false
	}
	return last - w.first.EdgeCounts[id], true
}

// Analyze reads a flow's samples, oldest first, and returns its findings
// ranked most urgent first.
func Analyze(samples []Sample, flow Flow) Report {
	r := Report{WorkflowID: flow.ID, State: StateNotRunning, Findings: []Finding{}}
	run := currentRun(samples)
	if len(run) == 0 {
		return r
	}
	w := window{s: run, first: run[0], last: run[len(run)-1]}
	w.span = w.last.At.Sub(w.first.At)
	r.WindowStart, r.WindowEnd, r.Samples = w.first.At, w.last.At, len(run)
	r.Processed = w.last.Processed - w.first.Processed
	if w.span > 0 {
		r.Throughput = float64(r.Processed) / w.span.Seconds()
	}
	r.State = StateCollecting
	if w.ready() {
		r.State = StateReady
	}
	r.Findings, r.BottleneckNodeID = findings(w, flow, r.State == StateReady, r.Processed)

	slices.SortStableFunc(r.Findings, func(a, b Finding) int {
		return cmp.Or(
			cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)),
			cmp.Compare(b.score, a.score),
			cmp.Compare(a.Rule, b.Rule),
		)
	})
	return r
}

// findings runs every rule that applies. Rules that read one reading or the
// flow's design run on any window; rules that compare readings over time run
// only once the window is ready.
func findings(w window, flow Flow, ready bool, processed uint64) ([]Finding, string) {
	out := []Finding{}
	add := func(f *Finding) {
		if f != nil {
			out = append(out, *f)
		}
	}
	out = append(out, stuckSteps(w, flow)...)
	add(traceSampling(flow))
	// Breakers are read reading by reading, so a short window still counts.
	out = append(out, sinkBreakers(w, flow)...)

	bottleneckID := ""
	if ready {
		b := bottleneck(w, flow)
		add(b)
		if b != nil {
			bottleneckID = b.NodeID
		}
		out = append(out, filterEarlier(w, flow)...)
		out = append(out, sinkBackpressure(w, flow)...)
		out = append(out, stepErrors(w, flow)...)
		add(deadLetters(w, processed))
		add(sourceLag(w))
	}
	out = append(out, lookupCacheOffFindings(flow, bottleneckID)...)
	return out, bottleneckID
}

func severityRank(s string) int {
	switch s {
	case SeverityHigh:
		return 0
	case SeverityMedium:
		return 1
	}
	return 2
}

// timedStep is a step that does measurable work per message.
func timedStep(t string) bool { return t != "source" && t != "" }

// bottleneck names the step that takes the largest share of the time all
// steps spend on messages, when that share is large enough to matter.
func bottleneck(w window, flow Flow) *Finding {
	type cost struct {
		id    string
		lat   time.Duration
		runs  uint64
		total float64
	}
	var costs []cost
	var sum float64
	for _, n := range flow.Nodes {
		if !timedStep(n.Type) {
			continue
		}
		runs := w.runs(n.ID)
		if runs == 0 {
			continue
		}
		lat := w.latency(n.ID)
		c := cost{id: n.ID, lat: lat, runs: runs, total: float64(lat) * float64(runs)}
		costs = append(costs, c)
		sum += c.total
	}
	if len(costs) < 2 || sum == 0 {
		return nil
	}
	top := slices.MaxFunc(costs, func(a, b cost) int { return cmp.Compare(a.total, b.total) })
	share := top.total / sum
	if share < bottleneckShare || top.lat < minWorthTuning {
		return nil
	}
	sev := SeverityMedium
	if share >= bottleneckShareHigh {
		sev = SeverityHigh
	}
	n, _ := flow.node(top.id)
	name := flow.name(top.id)
	return &Finding{
		Rule:     RuleBottleneck,
		Severity: sev,
		NodeID:   top.id,
		Title:    name + " is the slowest step",
		Detail: fmt.Sprintf("%s takes %s of the time this flow's steps spend on messages: %s per message over %s runs in the last %s.",
			name, pct(share), dur(top.lat), count(top.runs), dur(w.span)),
		Recommendation: bottleneckAdvice(n),
		Evidence: []Evidence{
			{"Share of step time", pct(share)},
			{"Average per message", dur(top.lat)},
			{"Runs in window", count(top.runs)},
		},
		score: share,
	}
}

// bottleneckAdvice is what usually speeds up a step of this kind.
func bottleneckAdvice(n Node) string {
	switch {
	case isLookup(n.Type) || n.Type == "redis_lookup" || n.Type == "fuzzy_lookup":
		return "Keep the lookup's cache on and raise its ttl if the data allows it, so repeated keys skip the round trip. For a database lookup, make sure the key column is indexed."
	case n.Type == "execute_sql":
		return "Check the statement's query plan and index the columns it filters on; a statement per message is costly, so batch or cache where you can."
	case strings.HasPrefix(n.Type, "ai_") || n.Type == "mimir_ask" || n.Type == "ml_predict":
		return "Send only the messages that need the model (filter before this step), shorten the prompt, or pick a faster model."
	case n.Type == "rate_limit":
		return "This step slows messages down on purpose. If the destination can take more, raise the rate limit."
	case n.Type == "sink":
		return "The write to this destination is the slow part. Turn on or raise the sink's batch size so it writes several messages per call."
	}
	return "This step does the most work per message. Simplify it, or filter out messages that do not need it before they reach it."
}

// excludedFromFilter holds steps whose output is lower than their input by
// design, not because they drop messages.
var excludedFromFilter = map[string]bool{
	"source": true, "sink": true, "approval": true, "wait": true, "collect": true, "join": true,
}

// filterEarlier finds a step that drops most of its messages after an
// upstream step has already spent much more time on each of them.
func filterEarlier(w window, flow Flow) []Finding {
	out := map[string][]Edge{}
	in := map[string][]string{}
	for _, e := range flow.Edges {
		out[e.From] = append(out[e.From], e)
		in[e.To] = append(in[e.To], e.From)
	}
	var found []Finding
	for _, n := range flow.Nodes {
		if excludedFromFilter[n.Type] || len(out[n.ID]) == 0 {
			continue
		}
		runs := w.runs(n.ID)
		passed, ok := w.passed(out[n.ID])
		if runs < minWindowProcessed || !ok || passed >= runs {
			continue
		}
		drop := 1 - float64(passed)/float64(runs)
		if drop < filterDropShare {
			continue
		}
		costly, costlyLat := costliestUpstream(w, flow, in, n.ID)
		if costly == "" || costlyLat < minWorthTuning || costlyLat < w.latency(n.ID)*filterCostRatio {
			continue
		}
		saved := time.Duration(float64(costlyLat) * float64(runs-passed))
		name, upstream := flow.name(n.ID), flow.name(costly)
		found = append(found, Finding{
			Rule:     RuleFilterEarlier,
			Severity: SeverityMedium,
			NodeID:   n.ID,
			Title:    fmt.Sprintf("Run %s before %s", name, upstream),
			Detail: fmt.Sprintf("%s lets %s of messages through, so %s are dropped after %s has already spent %s on each of them.",
				name, pct(1-drop), pct(drop), upstream, dur(costlyLat)),
			Recommendation: fmt.Sprintf("If %s does not use what %s adds, move it ahead of %s. That would have saved about %s of step time in the last %s.",
				name, upstream, upstream, dur(saved), dur(w.span)),
			Evidence: []Evidence{
				{"Messages dropped", pct(drop)},
				{upstream + " per message", dur(costlyLat)},
				{"Step time that could be saved", dur(saved)},
			},
			score: saved.Seconds(),
		})
	}
	return found
}

// passed is the messages that left a step along its edges over the window,
// and false when any of those edges is not counted.
func (w window) passed(edges []Edge) (uint64, bool) {
	var n uint64
	for _, e := range edges {
		c, ok := w.edge(e.ID)
		if !ok {
			return 0, false
		}
		n += c
	}
	return n, true
}

// costliestUpstream is the slowest timed step anywhere upstream of id.
func costliestUpstream(w window, flow Flow, in map[string][]string, id string) (string, time.Duration) {
	var costly string
	var costlyLat time.Duration
	seen := map[string]bool{id: true}
	queue := slices.Clone(in[id])
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		queue = append(queue, in[cur]...)
		u, ok := flow.node(cur)
		if !ok || !timedStep(u.Type) || u.Type == "sink" {
			continue
		}
		if lat := w.latency(cur); lat > costlyLat {
			costly, costlyLat = cur, lat
		}
	}
	return costly, costlyLat
}

// sinkBackpressure finds sinks whose buffer stays full: the destination takes
// messages slower than they arrive.
func sinkBackpressure(w window, flow Flow) []Finding {
	var found []Finding
	for _, id := range sinkIDs(w) {
		full := 0
		var peak float64
		for _, s := range w.s {
			f := s.SinkFill[id]
			if f > fullBuffer {
				full++
			}
			peak = max(peak, f)
		}
		share := float64(full) / float64(len(w.s))
		if full < minFullReadings || share < fullBufferShare {
			continue
		}
		set := flow.Sinks[id]
		limit := flow.MaxInflight
		if limit <= 0 {
			limit = defaultBatch
		}
		var advice string
		switch {
		case set.BatchSize <= 1:
			advice = fmt.Sprintf("Turn on batching: set the sink's batch size to %d, so it writes many messages per call instead of one.", min(defaultBatch, limit))
		case set.BatchSize < limit:
			advice = fmt.Sprintf("Raise the sink's batch size from %d to %d. Hermod cannot fill a batch larger than the engine's in-flight limit (%d), so going higher does not help.",
				set.BatchSize, min(set.BatchSize*2, limit), limit)
		case set.ShardCount <= 1:
			advice = fmt.Sprintf("Batching is already at the engine's in-flight limit (%d). Split the sink's writes across parallel shards (a shard count of 2 to 4), or make the destination faster.", limit)
		default:
			advice = "Batching and sharding are already on, so the destination itself is the limit. Check its load, indexes and network."
		}
		name := flow.sinkName(id)
		found = append(found, Finding{
			Rule:     RuleSinkBackpressure,
			Severity: SeverityHigh,
			SinkID:   id,
			Title:    name + " cannot keep up",
			Detail: fmt.Sprintf("%s's buffer was more than %s full in %s of readings over the last %s. Messages wait for it, and upstream steps slow down to match.",
				name, pct(fullBuffer), pct(share), dur(w.span)),
			Recommendation: advice,
			Evidence: []Evidence{
				{"Readings with a full buffer", pct(share)},
				{"Fullest reading", pct(peak)},
				{"Batch size", batchLabel(set.BatchSize)},
			},
			score: share,
		})
	}
	return found
}

func batchLabel(n int) string {
	if n <= 1 {
		return "off"
	}
	return count(uint64(n))
}

// sinkBreakers counts how often each sink's circuit breaker opened.
func sinkBreakers(w window, flow Flow) []Finding {
	var found []Finding
	for _, id := range sinkIDs(w) {
		opened := 0
		prev := ""
		for _, s := range w.s {
			st := s.SinkBreaker[id]
			if st == "open" && prev != "open" {
				opened++
			}
			prev = st
		}
		if opened == 0 {
			continue
		}
		name := flow.sinkName(id)
		found = append(found, Finding{
			Rule:     RuleSinkBreaker,
			Severity: SeverityHigh,
			SinkID:   id,
			Title:    fmt.Sprintf("Writes to %s keep failing", name),
			Detail: fmt.Sprintf("%s's circuit breaker opened %d %s in the last %s: writes failed often enough that Hermod paused them.",
				name, opened, plural(opened, "time", "times"), dur(w.span)),
			Recommendation: "Check the destination is reachable and accepting writes (the flow's logs give the error). If failures are brief, raise the sink's retries or its breaker threshold.",
			Evidence:       []Evidence{{"Times opened", count(uint64(opened))}, {"Now", cmp.Or(w.last.SinkBreaker[id], "closed")}},
			score:          float64(opened),
		})
	}
	return found
}

// stepErrors finds steps that failed on a large share of their runs.
func stepErrors(w window, flow Flow) []Finding {
	var found []Finding
	for _, n := range flow.Nodes {
		runs, errs := w.runs(n.ID), w.errors(n.ID)
		if runs == 0 || errs < minStepErrors {
			continue
		}
		share := float64(errs) / float64(runs)
		if share < stepErrorShare {
			continue
		}
		sev := SeverityMedium
		if share >= stepErrorHigh {
			sev = SeverityHigh
		}
		name := flow.name(n.ID)
		found = append(found, Finding{
			Rule:     RuleStepErrors,
			Severity: sev,
			NodeID:   n.ID,
			Title:    name + " fails often",
			Detail:   fmt.Sprintf("%s failed on %s of its runs (%s of %s) in the last %s.", name, pct(share), count(errs), count(runs), dur(w.span)),
			Recommendation: "Open Runs and filter by failed to see the error. A bad record needs a fix in the data or the step's settings; " +
				"a remote service that fails briefly needs more retries.",
			Evidence: []Evidence{{"Failed runs", pct(share)}, {"Failures in window", count(errs)}},
			score:    share,
		})
	}
	return found
}

func deadLetters(w window, processed uint64) *Finding {
	n := w.last.DeadLetters - w.first.DeadLetters
	if n == 0 {
		return nil
	}
	sev := SeverityMedium
	share := 0.0
	if processed > 0 {
		share = float64(n) / float64(processed)
		if share >= deadLetterHigh {
			sev = SeverityHigh
		}
	}
	return &Finding{
		Rule:           RuleDeadLetters,
		Severity:       sev,
		Title:          "Messages are going to dead letters",
		Detail:         fmt.Sprintf("%s messages were dead-lettered in the last %s, %s of those processed.", count(n), dur(w.span), pct(share)),
		Recommendation: "Open Dead Letters: messages are grouped by cause, so the largest group is the fix that clears the most. Replay them once it is fixed.",
		Evidence:       []Evidence{{"Dead-lettered in window", count(n)}, {"Share of processed", pct(share)}},
		score:          share,
	}
}

// stuckSteps reads the latest reading for steps with work in them that have
// finished nothing for a long time.
func stuckSteps(w window, flow Flow) []Finding {
	var found []Finding
	ids := make([]string, 0, len(w.last.NodeBusy))
	for id := range w.last.NodeBusy {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		b := w.last.NodeBusy[id]
		since := time.Duration(b.SinceProgressMs) * time.Millisecond
		if b.InFlight <= 0 || since < stuckAfter {
			continue
		}
		name := flow.name(id)
		found = append(found, Finding{
			Rule:           RuleStuckStep,
			Severity:       SeverityHigh,
			NodeID:         id,
			Title:          name + " looks stuck",
			Detail:         fmt.Sprintf("%s has %d %s in progress and has not finished one for %s.", name, b.InFlight, plural(int(b.InFlight), "message", "messages"), dur(since)),
			Recommendation: "It is usually waiting on something outside Hermod: a remote call with no timeout, or a locked table. Set a timeout on the step, and check the service it calls.",
			Evidence:       []Evidence{{"In progress", count(uint64(b.InFlight))}, {"Since last finished", dur(since)}},
			score:          since.Seconds(),
		})
	}
	return found
}

// sourceLag finds a source falling behind while the sinks are not the cause.
func sourceLag(w window) *Finding {
	first, last := w.first.Lag, w.last.Lag
	if last < lagFloor || float64(last) < float64(first)*lagGrowth {
		return nil
	}
	rising, steps := 0, 0
	var fill float64
	for i := 1; i < len(w.s); i++ {
		steps++
		if w.s[i].Lag > w.s[i-1].Lag {
			rising++
		}
	}
	for _, s := range w.s {
		for _, f := range s.SinkFill {
			fill = max(fill, f)
		}
	}
	if steps == 0 || float64(rising)/float64(steps) < lagRisingShare || fill >= lagSinkCalm {
		return nil
	}
	return &Finding{
		Rule:     RuleSourceLag,
		Severity: SeverityMedium,
		Title:    "The source is falling behind",
		Detail: fmt.Sprintf("Source lag grew from %s to %s over the last %s while the sinks had room, so the time goes in reading or in the steps.",
			count(first), count(last), dur(w.span)),
		Recommendation: "Speed up the slowest step first (see above). If the steps are fast, raise the source's batch or poll size so it reads more per round trip.",
		Evidence:       []Evidence{{"Lag at start", count(first)}, {"Lag now", count(last)}},
		score:          float64(last) / max(float64(first), 1),
	}
}

func lookupCacheOffFindings(flow Flow, bottleneckID string) []Finding {
	var found []Finding
	for _, n := range flow.Nodes {
		if !lookupCacheOff(n) {
			continue
		}
		sev := SeverityLow
		if n.ID == bottleneckID {
			sev = SeverityMedium
		}
		name := flow.name(n.ID)
		found = append(found, Finding{
			Rule:           RuleLookupCacheOff,
			Severity:       sev,
			NodeID:         n.ID,
			Title:          name + " has its cache turned off",
			Detail:         name + "'s ttl is 0, so every message makes a round trip, even for a key it looked up a moment ago.",
			Recommendation: "Unless every lookup must see the latest value, set a ttl (for example 5m) so repeated keys are answered from memory.",
			Evidence:       []Evidence{{"ttl", "0 (cache off)"}},
		})
	}
	return found
}

func traceSampling(flow Flow) *Finding {
	if flow.TraceSampleRate > 0 {
		return nil
	}
	return &Finding{
		Rule:           RuleTraceSampling,
		Severity:       SeverityLow,
		Title:          "Runs are not being recorded",
		Detail:         "Trace sampling is off, so Runs cannot show how long each step took for a particular message.",
		Recommendation: "Set trace sampling to 1% to 5% in the flow's settings. It records a share of messages step by step, at a small storage cost.",
		Evidence:       []Evidence{{"Trace sampling", "off"}},
	}
}

// sinkIDs lists every sink that appears in the window, in a stable order.
func sinkIDs(w window) []string {
	seen := map[string]bool{}
	for _, s := range w.s {
		for id := range s.SinkFill {
			seen[id] = true
		}
		for id := range s.SinkBreaker {
			seen[id] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

func count(n uint64) string {
	s := strconv.FormatUint(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// dur rounds a duration to what a person reads: ms under a second, then
// seconds, then minutes.
func dur(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%d µs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f min", d.Minutes())
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}
