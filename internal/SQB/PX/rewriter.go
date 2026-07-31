package PX

import "fmt"

// Rewriter accumulates DAG mutations (stage replacements and removals) (stage replacements and removals)
// over a PipelineSpec. Passes record changes via Replace/Remove; a
// single Commit call atomically rebuilds Stages, Edges, and RootIdx
// with a consistent index map.
type Rewriter struct {
	spec       *PipelineSpec
	replaces   map[int]StageSpec  // old stage idx → replacement stage
	removes    map[int]struct{}   // old stage idx → removed
	rootHint   int                // index of a Replace that claimed root (-1 = unset)
	hasRootHint bool             // true when a Replace(rootHint=true) was made
}

// NewRewriter creates a Rewriter for the given spec.
func NewRewriter(spec *PipelineSpec) *Rewriter {
	return &Rewriter{
		spec:     spec,
		replaces: make(map[int]StageSpec),
		removes:  make(map[int]struct{}),
	}
}

// Replace records a replacement for the stage at idx. If rootHint is
// true the new stage becomes the pipeline root. The replacement is
// not applied until Commit.
func (r *Rewriter) Replace(idx int, newStage StageSpec, rootHint bool) error {
	if idx < 0 || idx >= len(r.spec.Stages) {
		return fmt.Errorf("px.rewriter: Replace out of range %d (stages: %d)", idx, len(r.spec.Stages))
	}
	if _, removed := r.removes[idx]; removed {
		return fmt.Errorf("px.rewriter: Replace on removed stage %d", idx)
	}
	r.replaces[idx] = newStage
	if rootHint {
		r.rootHint = idx
		r.hasRootHint = true
	}
	return nil
}

// Remove records that the stage at idx should be deleted. The stage is
// not removed until Commit. Edges pointing to a removed stage are
// dropped during commit.
func (r *Rewriter) Remove(idx int) {
	if idx < 0 || idx >= len(r.spec.Stages) {
		return
	}
	if _, replaced := r.replaces[idx]; replaced {
		return // already being replaced; removal is superseded
	}
	r.removes[idx] = struct{}{}
}

// Commit atomically applies all recorded replacements and removals.
// Stages is rebuilt; Edges are remapped through the new index table;
// RootIdx is updated if a replacement was marked rootHint.
func (r *Rewriter) Commit() error {
	n := len(r.spec.Stages)
	if n == 0 {
		return nil
	}

	// Build new stages and old→new index map in one pass.
	newStages := make([]StageSpec, 0, n)
	oldToNew := make(map[int]int, n)
	newRootIdx := -1

	for i, stage := range r.spec.Stages {
		if _, removed := r.removes[i]; removed {
			continue
		}
		newIdx := len(newStages)
		if repl, replaced := r.replaces[i]; replaced {
			newStages = append(newStages, repl)
		} else {
			newStages = append(newStages, stage)
		}
		oldToNew[i] = newIdx

		if i == r.spec.RootIdx {
			newRootIdx = newIdx
		}
	}

	// Handle rootHint: a replacement that claimed root ownership overrides
	// the original root. Otherwise preserve whatever newRootIdx was
	// discovered (the remapped original root). If no root survived
	// (all stages removed), pick the last remaining stage.
	if r.hasRootHint {
		if ni, ok := oldToNew[r.rootHint]; ok {
			newRootIdx = ni
		}
	}
	if newRootIdx == -1 && len(newStages) > 0 {
		newRootIdx = len(newStages) - 1
	}

	// Remap edges.
	newEdges := make([]EdgeSpec, 0, len(r.spec.Edges))
	for _, edge := range r.spec.Edges {
		ni, ok1 := oldToNew[edge.From]
		ti, ok2 := oldToNew[edge.To]
		if !ok1 || !ok2 {
			continue // either end was removed; drop the edge
		}
		newEdges = append(newEdges, EdgeSpec{
			From: ni,
			To:   ti,
			Side: edge.Side,
		})
	}

	r.spec.Stages = newStages
	r.spec.Edges = newEdges
	if newRootIdx >= 0 && newRootIdx < len(newStages) {
		r.spec.RootIdx = newRootIdx
	}

	// Reset sentinels for reuse.
	r.replaces = make(map[int]StageSpec)
	r.removes = make(map[int]struct{})
	r.rootHint = -1
	r.hasRootHint = false
	return nil
}
