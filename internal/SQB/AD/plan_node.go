package AD

import (
	"context"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	"fmt"
	"math"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

type PlanNode struct {
	Type        string
	Table       string
	Index       string
	Cost        float64
	Rows        int64
	Width       int
	Detail      string
	Children    []*PlanNode
	Analyze     *AnalyzeStats
	Bottleneck  *BottleneckInfo
	IndexHint   *IndexHint
	Subquery    *SubqueryInfo
	TxnDebug    *UT.TxnDebugInfo
	Cache       *CacheInfo
}

type AnalyzeStats struct {
	RowsReturned int64
	TimeNS       int64
	Allocs       int64
}

type TableStats struct {
	RowCount      int64
	ColStats      map[string]*ls.ColumnStats
	TotalWidth    int
	LastAnalyzed  int64
}

type BottleneckInfo struct {
	Severity        string
	Type           string
	Details        string
	Recommendations []string
	ActualRows     int64
	EstimatedRows  int64
	ActualTimeNS   int64
	CostRatio      float64
}

type IndexHint struct {
	Used         bool
	IndexName    string
	AvailableIdx []string
	MissingCols  []string
	Reason       string
}

type SubqueryInfo struct {
	Type           string
	Unnested       bool
	ExecutionCount int64
	Method         string
}

type CacheInfo struct {
	Hit       bool
	HitRate   float64
	Hits      int64
	Misses    int64
	Evictions int64
}

func (n *PlanNode) Add(child *PlanNode) {
	n.Children = append(n.Children, child)
}

func (n *PlanNode) SetBottleneck(bn *BottleneckInfo) {
	n.Bottleneck = bn
}

func (n *PlanNode) SetIndexHint(ih *IndexHint) {
	n.IndexHint = ih
}

func (n *PlanNode) SetSubquery(sq *SubqueryInfo) {
	n.Subquery = sq
}

func (n *PlanNode) SetTxnDebug(td *UT.TxnDebugInfo) {
	n.TxnDebug = td
}

func (n *PlanNode) SetCache(ci *CacheInfo) {
	n.Cache = ci
}

func FormatPlanTree(n *PlanNode, mode PS.ExplainMode) []DT.Row {
	if n == nil {
		return nil
	}

	var rows []DT.Row
	nextID := 1
	var walk func(node *PlanNode, parent int)
	walk = func(node *PlanNode, parent int) {
		if node == nil {
			return
		}

		id := nextID
		nextID++

		var detail string
		switch mode {
		case PS.ExplainQueryPlan:
			detail = explainQueryPlanDetail(node)
		default:
			detail = node.Detail
			if detail == "" {
				detail = node.Type
			}
			if node.Table != "" && !strings.Contains(detail, node.Table) {
				detail += " " + node.Table
			}
			if node.Index != "" {
				detail += " USING INDEX " + node.Index
			}
			if node.Cost > 0 {
				detail += fmt.Sprintf(" cost=%.2f", node.Cost)
			}
			if node.Rows > 0 {
				detail += fmt.Sprintf(" rows=%d", node.Rows)
			}
		}

		if a := node.Analyze; a != nil {
			detail += fmt.Sprintf(" (actual rows=%d time=%dns allocs=%d)", a.RowsReturned, a.TimeNS, a.Allocs)
		}
		if bn := node.Bottleneck; bn != nil {
			detail += fmt.Sprintf(" [BOTTLENECK: %s severity=%s]", bn.Type, bn.Severity)
			if bn.Details != "" {
				detail += fmt.Sprintf(" (%s)", bn.Details)
			}
			if len(bn.Recommendations) > 0 {
				detail += fmt.Sprintf(" recommend: %s", strings.Join(bn.Recommendations, "; "))
			}
		}
		if ih := node.IndexHint; ih != nil {
			if ih.Used {
				detail += fmt.Sprintf(" [INDEX: %s used]", ih.IndexName)
			} else {
				detail += fmt.Sprintf(" [INDEX: skipped — %s]", ih.Reason)
				if len(ih.MissingCols) > 0 {
					detail += fmt.Sprintf(" consider index on (%s)", strings.Join(ih.MissingCols, ", "))
				}
			}
		}
		if sq := node.Subquery; sq != nil {
			if sq.Unnested {
				detail += fmt.Sprintf(" [SUBQUERY: unnested → %s]", sq.Method)
			} else {
				detail += fmt.Sprintf(" [SUBQUERY: %s executed %d times]", sq.Type, sq.ExecutionCount)
				if sq.ExecutionCount > 1000 {
					detail += " WARNING: consider rewriting as JOIN"
				}
			}
		}
		if td := node.TxnDebug; td != nil {
			detail += fmt.Sprintf(" [MVCC: visible=%d hidden=%d snapshot=%d]", td.VisibleRows, td.HiddenByMVCC, td.SnapshotTS)
			if td.LockWaitTimeNS > 0 {
				detail += fmt.Sprintf(" lock_wait=%dns", td.LockWaitTimeNS)
			}
		}
		if ci := node.Cache; ci != nil {
			if ci.Hit {
				detail += fmt.Sprintf(" [CACHE: hit (rate=%.1f%%)]", ci.HitRate)
			} else {
				detail += fmt.Sprintf(" [CACHE: miss (hits=%d misses=%d rate=%.1f%%)]", ci.Hits, ci.Misses, ci.HitRate)
			}
		}

		rows = append(rows, DT.Row{
			Cols:  []string{"id", "parent", "notused", "detail"},
			Types: []LX.TokenType{LX.T_INT_KW, LX.T_INT_KW, LX.T_INT_KW, LX.T_TEXT},
			Data:  []DT.Value{AP.NewIntValue(int64(id)), AP.NewIntValue(int64(parent)), AP.NewIntValue(0), AP.NewTextValue(detail)},
		})

		for _, child := range node.Children {
			walk(child, id)
		}
	}

	walk(n, 0)
	return rows
}

func AnalyzePlanForBottlenecks(root *PlanNode) []*BottleneckInfo {
	var bottlenecks []*BottleneckInfo
	if root == nil {
		return bottlenecks
	}

	var walk func(node *PlanNode)
	walk = func(node *PlanNode) {
		if node == nil {
			return
		}
		if bn := identifyBottleneck(node); bn != nil {
			node.Bottleneck = bn
			bottlenecks = append(bottlenecks, bn)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return bottlenecks
}

func identifyBottleneck(node *PlanNode) *BottleneckInfo {
	if node.Analyze == nil {
		return nil
	}
	var costRatio float64
	if node.Cost > 0 {
		costRatio = float64(node.Analyze.TimeNS) / node.Cost
	} else {
		costRatio = float64(node.Analyze.TimeNS) / 1000
	}
	bn := &BottleneckInfo{
		Severity:     "low",
		Type:         "general",
		ActualRows:   node.Analyze.RowsReturned,
		EstimatedRows: node.Rows,
		ActualTimeNS: node.Analyze.TimeNS,
		CostRatio:    costRatio,
	}
	if costRatio > 1000 {
		bn.Severity = "critical"
		if node.Type == "Scan" {
			bn.Type = "seq_scan"
			bn.Details = fmt.Sprintf("Seq scan on %s took %dns", node.Table, node.Analyze.TimeNS)
			bn.Recommendations = []string{"Consider adding an index"}
		} else if node.Type == "Join" || node.Type == "OP.HashJoin" {
			bn.Type = "hash_join_fallback"
			bn.Details = fmt.Sprintf("HashJoin on %s took %dns", node.Table, node.Analyze.TimeNS)
			bn.Recommendations = []string{"Consider increasing work_mem", "Check join ordering"}
		} else {
			bn.Details = fmt.Sprintf("High execution time: %dns", node.Analyze.TimeNS)
			bn.Recommendations = []string{"Review query plan for optimization opportunities"}
		}
	} else if costRatio > 100 {
		bn.Severity = "high"
		bn.Details = fmt.Sprintf("Elevated execution time: %dns", node.Analyze.TimeNS)
		if node.Type == "Sort" {
			bn.Type = "large_sort"
			bn.Recommendations = []string{"Consider adding an index to avoid sort"}
		}
	} else if costRatio > 10 {
		bn.Severity = "medium"
	}
	return bn
}

func explainQueryPlanDetail(n *PlanNode) string {
	switch n.Type {
	case "Scan":
		if n.Index != "" {
			return "SEARCH " + n.Table + " USING INDEX " + n.Index
		}
		return "SCAN " + n.Table
	case "Search":
		return "SEARCH " + n.Table + " USING INDEX " + n.Index
	case "Join":
		return "JOIN " + n.Table
	case "OP.HashJoin":
		return "HASH JOIN " + n.Table
	case "MergeJoin":
		return "MERGE JOIN " + n.Table
	case "HashCrossJoin":
		return "HASH CROSS JOIN " + n.Table
	case "IndexOnlyScan":
		return "INDEX ONLY SCAN " + n.Table
	case "OP.Filter":
		return "FILTER"
	case "OP.Project":
		return "PROJECT"
	case "OP.Sort":
		return "SORT"
	case "OP.Distinct":
		return "DISTINCT"
	case "Aggregate":
		return "AGGREGATE"
	case "OP.Limit":
		return "LIMIT"
	case "OP.Offset":
		return "OFFSET"
	case "OP.Values":
		return "VALUES"
	case "Insert":
		return "INSERT"
	case "Update":
		return "UPDATE"
	case "Delete":
		return "DELETE"
	default:
		return strings.ToUpper(n.Type)
	}
}

func EstimateFilterCost(f *OP.Filter, ts *TableStats) float64 {
	if f.Child() == nil {
		return 1.0
	}
	selectivity := 0.5
	if ts != nil && ts.RowCount > 0 {
		selectivity = 0.5
	}
	return selectivity
}

func EstimateProjectCost(p *OP.Project) float64 {
	if p.Child() == nil {
		return 1.0
	}
	return 1.0
}

func EstimateSortCost(s *OP.Sort, ts *TableStats) float64 {
	if s.Child() == nil {
		return 1.0
	}
	inputRows := 100.0
	if ts != nil && ts.RowCount > 0 {
		inputRows = float64(ts.RowCount)
	}
	return 10.0 * (1 + math.Log2(inputRows+1))
}

func EstimateLimitCost(l *OP.Limit) float64 {
	if l.Child() == nil {
		return 1.0
	}
	return 1.0
}

func EstimateOffsetCost(o *OP.Offset) float64 {
	if o.Child() == nil {
		return 1.0
	}
	return 1.0
}

func EstimateDistinctCost(d *OP.Distinct, ts *TableStats) float64 {
	if d.Child() == nil {
		return 1.0
	}
	inputRows := 100.0
	if ts != nil && ts.RowCount > 0 {
		inputRows = float64(ts.RowCount)
	}
	return 2.0 + inputRows
}

func EstimateAggregateCost(a *AG.Aggregate, ts *TableStats) float64 {
	if a.Child() == nil {
		return 1.0
	}
	inputRows := 100.0
	if ts != nil && ts.RowCount > 0 {
		inputRows = float64(ts.RowCount)
	}
	return 5.0 + inputRows
}

func EstimateIndexCost(ts *TableStats, indexCols []string) float64 {
	if ts == nil || ts.RowCount == 0 {
		return 10.0
	}
	totalDistinct := int64(1)
	for _, col := range indexCols {
		if cs, ok := ts.ColStats[col]; ok {
			if cs.DistinctCount > 0 {
				totalDistinct *= cs.DistinctCount
			}
		}
	}
	selectivity := float64(ts.RowCount) / float64(totalDistinct)
	if selectivity < 1.0 {
		selectivity = 1.0
	}
	return selectivity
}

func EstimateJoinCost(j *OP.NestedLoopJoin, leftTS, rightTS *TableStats) float64 {
	leftCost := 1.0
	rightCost := 1.0
	if j.LeftChild() != nil {
		if leftTS != nil && leftTS.RowCount > 0 {
			leftCost = float64(leftTS.RowCount)
		}
	}
	if j.RightChild() != nil {
		if rightTS != nil && rightTS.RowCount > 0 {
			rightCost = float64(rightTS.RowCount)
		}
	}
	return leftCost * rightCost
}

func (n *PlanNode) ToJSON() string {
	result := planNodeToJSON(n)
	var b strings.Builder
	buildJSON(&b, result)
	return b.String()
}

func planNodeToJSON(node *PlanNode) *planNodeJSON {
	if node == nil {
		return nil
	}
	jn := &planNodeJSON{
		Type:   node.Type,
		Table:  node.Table,
		Index:  node.Index,
		Cost:   node.Cost,
		Rows:   node.Rows,
		Detail: node.Detail,
	}
	if len(node.Children) > 0 {
		for _, child := range node.Children {
			jn.Children = append(jn.Children, planNodeToJSON(child))
		}
	}
	if node.Analyze != nil {
		jn.Analyze = &analyzeStatsJSON{
			RowsReturned: node.Analyze.RowsReturned,
			TimeNS:       node.Analyze.TimeNS,
			Allocs:       node.Analyze.Allocs,
		}
	}
	if node.Bottleneck != nil {
		jn.Bottleneck = &bottleneckJSON{
			Severity:        node.Bottleneck.Severity,
			Type:            node.Bottleneck.Type,
			Details:         node.Bottleneck.Details,
			Recommendations: node.Bottleneck.Recommendations,
			ActualRows:      node.Bottleneck.ActualRows,
			EstimatedRows:   node.Bottleneck.EstimatedRows,
			ActualTimeNS:    node.Bottleneck.ActualTimeNS,
			CostRatio:       node.Bottleneck.CostRatio,
		}
	}
	return jn
}

func buildJSON(b *strings.Builder, jn *planNodeJSON) {
	if jn == nil {
		b.WriteString("null")
		return
	}
	b.WriteByte('{')
	if jn.ID != 0 {
		b.WriteString(`"id":`)
		b.WriteString(fmt.Sprintf("%d", jn.ID))
		b.WriteByte(',')
	}
	b.WriteString(`"type":"`)
	b.WriteString(escapeJSON(jn.Type))
	b.WriteByte('"')
	if jn.Table != "" {
		b.WriteString(`,"table":"`)
		b.WriteString(escapeJSON(jn.Table))
		b.WriteByte('"')
	}
	if jn.Index != "" {
		b.WriteString(`,"index":"`)
		b.WriteString(escapeJSON(jn.Index))
		b.WriteByte('"')
	}
	if jn.Cost > 0 {
		b.WriteString(`,"cost":`)
		b.WriteString(fmt.Sprintf("%g", jn.Cost))
	}
	if jn.Rows > 0 {
		b.WriteString(`,"rows":`)
		b.WriteString(fmt.Sprintf("%d", jn.Rows))
	}
	if jn.Detail != "" {
		b.WriteString(`,"detail":"`)
		b.WriteString(escapeJSON(jn.Detail))
		b.WriteByte('"')
	}
	if len(jn.Children) > 0 {
		b.WriteString(`,"children":[`)
		for i, child := range jn.Children {
			if i > 0 {
				b.WriteByte(',')
			}
			buildJSON(b, child)
		}
		b.WriteByte(']')
	}
	if jn.Analyze != nil {
		b.WriteString(`,"analyze":{"rows_returned":`)
		b.WriteString(fmt.Sprintf("%d", jn.Analyze.RowsReturned))
		b.WriteString(`,"time_ns":`)
		b.WriteString(fmt.Sprintf("%d", jn.Analyze.TimeNS))
		b.WriteString(`,"allocs":`)
		b.WriteString(fmt.Sprintf("%d", jn.Analyze.Allocs))
		b.WriteByte('}')
	}
	if jn.Bottleneck != nil {
		b.WriteString(`,"bottleneck":{"severity":"`)
		b.WriteString(escapeJSON(jn.Bottleneck.Severity))
		b.WriteByte('"')
		b.WriteString(`,"type":"`)
		b.WriteString(escapeJSON(jn.Bottleneck.Type))
		b.WriteByte('"')
		if jn.Bottleneck.Details != "" {
			b.WriteString(`,"details":"`)
			b.WriteString(escapeJSON(jn.Bottleneck.Details))
			b.WriteByte('"')
		}
		if len(jn.Bottleneck.Recommendations) > 0 {
			b.WriteString(`,"recommendations":[`)
			for i, rec := range jn.Bottleneck.Recommendations {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(`"`)
				b.WriteString(escapeJSON(rec))
				b.WriteByte('"')
			}
			b.WriteByte(']')
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
}

func (n *PlanNode) ToDOT() string {
	var b strings.Builder
	b.WriteString("digraph plan {\n")
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  node [shape=box, style=filled];\n")

	nodeID := 0
	var walk func(node *PlanNode) string
	walk = func(node *PlanNode) string {
		if node == nil {
			return ""
		}
		id := nodeID
		nodeID++

		label := node.Type
		if node.Table != "" {
			label += "\\n" + node.Table
		}
		if node.Detail != "" {
			label += "\\n" + node.Detail
		}
		if node.Cost > 0 {
			label += "\\ncost=" + fmt.Sprintf("%.2f", node.Cost)
		}
		if node.Rows > 0 {
			label += "\\nrows=" + fmt.Sprintf("%d", node.Rows)
		}
		if node.Analyze != nil {
			label += "\\n(actual=" + fmt.Sprintf("%d", node.Analyze.RowsReturned) + ")"
		}

		b.WriteString(fmt.Sprintf("  n%d [label=\"%s\"];\n", id, escapeDOT(label)))
		for _, child := range node.Children {
			childID := walk(child)
			if childID != "" {
				b.WriteString(fmt.Sprintf("  n%d -> n%s;\n", id, childID))
			}
		}
		return fmt.Sprintf("%d", id)
	}

	walk(n)
	b.WriteString("}\n")
	return b.String()
}

func (n *PlanNode) ToTree() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(node *PlanNode, prefix string, isLast bool)
	walk = func(node *PlanNode, prefix string, isLast bool) {
		if node == nil {
			return
		}
		connector := "└── "
		if !isLast {
			connector = "├── "
		}
		if prefix == "" {
			connector = ""
		}
		line := connector
		if node.Type != "" {
			line += node.Type
		}
		if node.Table != "" {
			line += " " + node.Table
		}
		if node.Index != "" {
			line += " [idx:" + node.Index + "]"
		}
		if node.Detail != "" && node.Detail != node.Type {
			line += " " + node.Detail
		}
		if node.Cost > 0 {
			line += " cost=" + fmt.Sprintf("%.2f", node.Cost)
		}
		if node.Rows > 0 {
			line += " rows=" + fmt.Sprintf("%d", node.Rows)
		}
		if node.Analyze != nil {
			line += " (actual=" + fmt.Sprintf("%d", node.Analyze.RowsReturned) + " time=" + fmt.Sprintf("%d", node.Analyze.TimeNS) + "ns)"
		}
		if node.Bottleneck != nil {
			line += " [BOTTLENECK: " + node.Bottleneck.Type + "]"
		}
		b.WriteString(line + "\n")

		newPrefix := prefix
		if prefix != "" {
			if isLast {
				newPrefix += "    "
			} else {
				newPrefix += "│   "
			}
		}
		for i, child := range node.Children {
			isLastChild := i == len(node.Children)-1
			walk(child, newPrefix, isLastChild)
		}
	}
	walk(n, "", true)
	return b.String()
}

func escapeDOT(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

type analyzeStatsJSON struct {
	RowsReturned int64 `json:"rows_returned"`
	TimeNS       int64 `json:"time_ns"`
	Allocs       int64 `json:"allocs"`
}

type bottleneckJSON struct {
	Severity        string   `json:"severity"`
	Type            string   `json:"type"`
	Details         string   `json:"details"`
	Recommendations []string `json:"recommendations"`
	ActualRows      int64    `json:"actual_rows"`
	EstimatedRows   int64    `json:"estimated_rows"`
	ActualTimeNS    int64    `json:"actual_time_ns"`
	CostRatio       float64  `json:"cost_ratio"`
}

type planNodeJSON struct {
	ID         int              `json:"id"`
	Type       string           `json:"type"`
	Table      string           `json:"table,omitempty"`
	Index      string           `json:"index,omitempty"`
	Cost       float64          `json:"cost,omitempty"`
	Rows       int64            `json:"rows,omitempty"`
	Detail     string           `json:"detail,omitempty"`
	Children   []*planNodeJSON  `json:"children,omitempty"`
	Analyze    *analyzeStatsJSON `json:"analyze,omitempty"`
	Bottleneck *bottleneckJSON   `json:"bottleneck,omitempty"`
}

type Noop struct{}

var _ DT.Operator = (*Noop)(nil)

func NewNoop() *Noop {
	return &Noop{}
}
func (n *Noop) Next(ctx context.Context) (DT.Row, error) {
	return DT.Row{}, DT.ErrNoRows
}
func (n *Noop) Close() error {
	return nil
}
func (n *Noop) WithParams(p []any) DT.Operator {
	return n
}