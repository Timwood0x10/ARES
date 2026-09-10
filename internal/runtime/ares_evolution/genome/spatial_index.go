package genome

import (
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// spatialIndex provides grid-based spatial hashing for fast nearest-neighbor
// queries in normalized parameter space. Each agent is assigned to a grid cell;
// neighbor queries only check the same and adjacent cells, achieving sub-linear
// lookup when the population is spread across many cells.
//
// The grid cell size equals the niche radius, so any agent within niche radius
// must be in the same or an immediately adjacent cell (Manhattan distance <= 1).
//
// For high-dimensional param spaces (nDim > 6), the number of adjacent cells
// grows as 3^nDim which defeats the purpose. In that case, we pick the top-N
// highest-variance dimensions and project agents onto that subspace for grid
// assignment, while distance calculations still use the full parameter space.
type spatialIndex struct {
	cellSize  float64
	nDim      int // effective dimensions used for grid
	maxDim    int // total float param dimensions
	floatKeys []string
	ranges    map[string]float64 // per-dim population range (max-min), same normalization paramDistance uses
	cellMap   map[string][]int   // cell key → agent indices (into scored slice)
	scored    []*mutation.Strategy
	scoredIdx []int
}

// maxSpatialDims caps the grid dimensionality to keep adjacent-cell checks
// tractable. 3^6 = 729 adjacent cells per query is manageable; 3^7 = 2187 is not.
const maxSpatialDims = 6

// newSpatialIndex builds a spatial index over the scored agent population.
// Returns nil when there are too few float parameters to benefit.
func newSpatialIndex(scoredIdx []int, scored []*mutation.Strategy, keys []string, ranges map[string]float64, cellSize float64) *spatialIndex {
	// Collect float64 parameter keys (only these have a spatial ordering).
	floatKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := scored[0].Params[k].(float64); ok {
			floatKeys = append(floatKeys, k)
		}
	}
	if len(floatKeys) < 2 {
		return nil // spatial indexing needs at least 2 dimensions to cluster
	}

	// For high-dimensional spaces, select the top-N highest-variance dimensions.
	nFloat := len(floatKeys)
	if nFloat > maxSpatialDims {
		floatKeys = selectTopVarDims(scored, floatKeys, ranges, maxSpatialDims)
	}

	nDim := len(floatKeys)
	idx := &spatialIndex{
		cellSize:  cellSize,
		nDim:      nDim,
		maxDim:    nFloat,
		floatKeys: floatKeys,
		ranges:    ranges,
		cellMap:   make(map[string][]int),
		scored:    scored,
		scoredIdx: scoredIdx,
	}

	for si := range scored {
		key := idx.cellKey(si)
		idx.cellMap[key] = append(idx.cellMap[key], si)
	}

	return idx
}

// cellKey returns the grid cell key for agent at scoredIdx.
func (idx *spatialIndex) cellKey(si int) string {
	cell := make([]byte, idx.nDim)
	for di, k := range idx.floatKeys {
		val, _ := idx.scored[si].Params[k].(float64)
		// Normalize by the population range BEFORE quantizing: cellSize
		// (the niche radius) lives in the same normalized space paramDistance
		// measures (per-dim |a-b|/range), so quantizing raw values would put
		// a wide-range dim (e.g. max_tokens 0..4096) thousands of cells away
		// from a normalized-close neighbor, and the adjacent-cell neighbor
		// scan would silently miss it.
		r := idx.ranges[k]
		if r < 1e-10 {
			r = 1.0
		}
		coord := idx.quantize(val / r)
		cell[di] = coord
	}
	return string(cell)
}

// quantize maps a normalized value to a byte cell coordinate. Each cell spans
// cellSize in normalized units (round-to-nearest via the +0.5 shift), so two
// values less than cellSize apart always land in the same or adjacent cells.
// Values are clamped at the 0/255 boundaries; clamping can only MERGE distant
// values into a boundary cell (a false candidate the exact distance re-check
// filters out), never separate close ones.
func (idx *spatialIndex) quantize(val float64) byte {
	normalized := val/idx.cellSize + 0.5
	if normalized <= 0 {
		return 0
	}
	if normalized >= 256 {
		return 255
	}
	return byte(normalized)
}

// neighborsWithin returns indices (into the scored slice) that are in the same
// or immediately adjacent cells as the given agent. Cell coordinates are
// computed in the SAME range-normalized space paramDistance measures, so any
// candidate the adjacent-cell scan returns is verified with the exact distance
// check by the caller; paramDistance averages per-dim normalized diffs (and
// adds categorical terms), so a pair within nicheRadius can occasionally be
// more than one cell apart in a single grid dim — the grid is a recall
// pre-filter, not an exact neighbor oracle, matching the sampled mode's
// bounded-neighbor approximation.
func (idx *spatialIndex) neighborsWithin(si int) []int {
	center := idx.cellKey(si)
	visited := make(map[int]bool)
	var result []int

	// Check the agent's own cell.
	idx.collectCell(center, visited, &result)

	// Check adjacent cells for each dimension independently.
	if idx.nDim <= 3 {
		// For 2-3 dim, exhaustively check all 3^nDim - 1 adjacent cells.
		idx.collectAdjacentCells(center, visited, &result)
	} else {
		// For 4-6 dim, check only the most promising adjacent cells:
		// those differing in exactly one dimension by ±1.
		idx.collectAdjacentCellsSparse(center, visited, &result)
	}

	return result
}

func (idx *spatialIndex) collectCell(key string, visited map[int]bool, result *[]int) {
	cell, ok := idx.cellMap[key]
	if !ok {
		return
	}
	for _, si := range cell {
		if !visited[si] {
			visited[si] = true
			*result = append(*result, si)
		}
	}
}

// collectAdjacentCells recursively visits all adjacent cells (Manhattan distance <= 1).
// For nDim=2: up to 8 adjacent cells. For nDim=3: up to 26 adjacent cells.
func (idx *spatialIndex) collectAdjacentCells(center string, visited map[int]bool, result *[]int) {
	base := []byte(center)
	if len(base) != idx.nDim {
		return
	}

	var recurse func(dim int, cell []byte)
	recurse = func(dim int, cell []byte) {
		if dim == idx.nDim {
			if string(cell) == center {
				return // skip the center cell (already collected)
			}
			idx.collectCell(string(cell), visited, result)
			return
		}
		// Current dim value.
		c := base[dim]

		// Try current, previous, and next.
		for _, offset := range []int{0, -1, 1} {
			nc := int(c) + offset
			if nc < 0 {
				continue
			}
			if nc > 255 {
				continue
			}
			cell[dim] = byte(nc)
			recurse(dim+1, cell)
		}
	}

	cell := make([]byte, idx.nDim)
	copy(cell, base)
	recurse(0, cell)
}

// collectAdjacentCellsSparse checks only single-dimension ±1 neighbors
// (Manhattan distance = 1). This covers 2×nDim adjacent cells instead of 3^nDim.
func (idx *spatialIndex) collectAdjacentCellsSparse(center string, visited map[int]bool, result *[]int) {
	base := []byte(center)
	for dim := 0; dim < idx.nDim; dim++ {
		for _, offset := range []int{-1, 1} {
			nc := int(base[dim]) + offset
			if nc < 0 || nc > 255 {
				continue
			}
			adj := make([]byte, idx.nDim)
			copy(adj, base)
			adj[dim] = byte(nc)
			idx.collectCell(string(adj), visited, result)
		}
	}
}

// selectTopVarDims picks the `n` float parameters with the highest variance
// across the scored population. Variance is measured on range-normalized
// values: raw variance is dimensionally inconsistent (a dim spanning 0..4096
// always out-varies one spanning 0..1 regardless of actual spread), so the
// grid would be projected onto whatever dims merely have the largest units.
func selectTopVarDims(scored []*mutation.Strategy, floatKeys []string, ranges map[string]float64, n int) []string {
	type dimVar struct {
		key      string
		variance float64
	}

	dims := make([]dimVar, len(floatKeys))
	for di, k := range floatKeys {
		r := ranges[k]
		if r < 1e-10 {
			r = 1.0
		}
		var sum, sumSq float64
		count := 0
		for _, s := range scored {
			if v, ok := s.Params[k].(float64); ok {
				norm := v / r
				sum += norm
				sumSq += norm * norm
				count++
			}
		}
		if count > 1 {
			mean := sum / float64(count)
			dims[di] = dimVar{key: k, variance: sumSq/float64(count) - mean*mean}
		} else {
			dims[di] = dimVar{key: k, variance: 0}
		}
	}

	// Sort by variance descending (simple insertion sort for small n).
	for i := 1; i < len(dims); i++ {
		for j := i; j > 0 && dims[j].variance > dims[j-1].variance; j-- {
			dims[j], dims[j-1] = dims[j-1], dims[j]
		}
	}

	selected := make([]string, 0, n)
	for _, d := range dims[:min(n, len(dims))] {
		selected = append(selected, d.key)
	}
	return selected
}
