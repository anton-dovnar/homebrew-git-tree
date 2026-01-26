package view

import (
	"crypto/md5"
	"fmt"
	"image/color"
	"sort"

	svg "github.com/ajstarks/svgo"
	"github.com/anton-dovnar/git-tree/structs"
	"github.com/go-git/go-git/v5/plumbing"

	mapset "github.com/deckarep/golang-set/v2"
)

const (
	scale     = 1.5
	stepX     = 24
	stepY     = 30
	paddingX  = 50
	paddingY  = 8
	stopR     = 5
	railW     = 6
	refAngle  = 0
	maxColors = 32
)

type SVGCommit struct {
	Hash    string
	X, Y    int
	Message string
	Refs    []string        // Branch references
	Tags    []string        // Tag references
	Parents []plumbing.Hash // Parent commit hashes
	Heads   []string        // Head references
}

type SVGRailway struct {
	*svg.SVG
	colors map[string]color.RGBA
}

func NewSVGRailway(canvas *svg.SVG) *SVGRailway {
	return &SVGRailway{
		SVG:    canvas,
		colors: make(map[string]color.RGBA),
	}
}

func (sr *SVGRailway) refToColor(ref string) color.RGBA {
	if color, exists := sr.colors[ref]; exists {
		return color
	}

	hash := md5.Sum([]byte(ref))
	h := float64(hash[0]) / 255.0
	s := 0.6 + (float64(hash[1])/255.0)*0.1
	l := 0.7 + (float64(hash[2])/255.0)*0.2

	color := hslToRGB(h, s, l)

	// fmt.Println("Ref: ", ref)
	// fmt.Println("Hash: ", hash)
	// fmt.Println("Color: ", color)

	sr.colors[ref] = color
	return color
}

func hslToRGB(h, s, l float64) color.RGBA {
	var r, g, b float64

	if s == 0 {
		r = l
		g = l
		b = l
	} else {
		var q, p float64
		if l < 0.5 {
			q = l * (1 + s)
		} else {
			q = l + s - l*s
		}
		p = 2*l - q

		r = hueToRGB(p, q, h+1.0/3)
		g = hueToRGB(p, q, h)
		b = hueToRGB(p, q, h-1.0/3)
	}

	return color.RGBA{
		R: uint8(r * 255),
		G: uint8(g * 255),
		B: uint8(b * 255),
		A: 255,
	}
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	default:
		return p
	}
}

func (sr *SVGRailway) Rail(x, y, px, py int, colors []color.RGBA, middle bool) {
	if len(colors) == 0 {
		// default single gray rail
		colors = []color.RGBA{{219, 219, 219, 255}}
	}

	n := len(colors)
	w := float64(railW) / float64(n)
	// horizontal offset for stacked rails so they center on the lattice x
	dX := -float64(n-1) / 2 * w

	// absolute coordinates for current and parent
	ax := paddingX + float64(x)*stepX
	ay := paddingY + float64(y)*stepY
	bx := paddingX + float64(px)*stepX
	by := paddingY + float64(py)*stepY

	dx := ax - bx

	for i, c := range colors {
		ox := dX + float64(i)*w
		startX := bx + ox
		startY := by

		path := fmt.Sprintf("M %.2f %.2f ", startX, startY)

		// If middle and x != px, draw a bezier/curved connector from parent to child,
		// otherwise draw a vertical line to child Y and horizontal if same X.
		if middle && dx != 0 {
			// relative control points scaled to dx/dy to make nicer curves
			cp1x := bx + dx*0.4
			cp1y := by
			cp2x := bx + dx*0.6
			cp2y := ay
			endX := ax + ox
			endY := ay
			// Use cubic Bezier path
			path += fmt.Sprintf("C %.2f %.2f %.2f %.2f %.2f %.2f", cp1x, cp1y, cp2x, cp2y, endX, endY)
		} else {
			// straight vertical/horizontal connection
			// First vertical from parent to child Y at parent's x offset, then horizontal to child's x
			path += fmt.Sprintf("L %.2f %.2f L %.2f %.2f", bx+ox, ay, ax+ox, ay)
		}

		strokeWidth := w
		sr.Path(path, fmt.Sprintf(`fill="none" stroke="%s" stroke-width="%.2f" stroke-linecap="round"`, colorToHex(c), strokeWidth))
	}
}

func (sr *SVGRailway) Stop(x, y int, c color.RGBA, commit SVGCommit) {
	cx := paddingX + x*stepX
	cy := paddingY + y*stepY

	// draw circle
	sr.Circle(cx, cy, stopR, fmt.Sprintf(`fill="%s" id="%s"`, colorToHex(c), commit.Hash))

	// labels
	sr.addLabels(x, y, commit)
}

func (sr *SVGRailway) addLabels(x, y int, commit SVGCommit) {
	tx := paddingX + x*stepX + paddingY
	ty := paddingY + y*stepY + 4

	// Commit hash (short)
	if len(commit.Hash) >= 7 {
		sr.Text(tx-paddingX-6, ty, commit.Hash[:7],
			`fill="#c9bcbc" font-family="Ubuntu Mono" font-size="12px"`)
	} else {
		sr.Text(tx-paddingX-6, ty, commit.Hash,
			`fill="#c9bcbc" font-family="Ubuntu Mono" font-size="12px"`)
	}

	// Heads / refs (draw to the right of hash)
	offset := 0
	for _, ref := range commit.Heads {
		color := sr.refToColor(ref)
		sr.Text(tx+offset, ty, ref,
			fmt.Sprintf(`fill="%s" font-family="Ubuntu Mono" font-size="14px" font-weight="bold"`, colorToHex(color)))
		offset += len(ref)*7 + 10
	}

	// Tags under the line (if any)
	tagOffset := 0
	for _, tag := range commit.Tags {
		sr.Text(tx+tagOffset, ty+14, tag,
			`fill="#dad682" font-family="Ubuntu Mono" font-size="12px" font-weight="bold"`)
		tagOffset += len(tag)*7 + 10
	}

	// Commit message (small, below)
	if commit.Message != "" {
		// Only show first line of message
		msgLine := commit.Message
		for i, ch := range msgLine {
			if ch == '\n' {
				msgLine = msgLine[:i]
				break
			}
		}
		sr.Text(tx, ty+28, msgLine,
			`fill="#a0a0a0" font-family="Ubuntu Condensed" font-size="11px"`)
	}
}

func colorToHex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

func convertToSVGCommits(
	commits map[plumbing.Hash]*structs.CommitInfo,
	positions map[plumbing.Hash][2]int,
	heads map[plumbing.Hash][]*plumbing.Reference,
	tags map[plumbing.Hash][]*plumbing.Reference,
) []SVGCommit {
	var svgCommits []SVGCommit
	for hash, ci := range commits {
		pos, ok := positions[hash]
		if !ok {
			// skip commits without position
			continue
		}
		// collect heads for this hash (convert *plumbing.Reference -> string)
		var headNames []string
		if hs, ok := heads[hash]; ok {
			for _, r := range hs {
				headNames = append(headNames, r.Name().Short())
			}
		}
		// collect refs from CommitInfo.References (use ToSlice)
		var refs []string
		if ci != nil && ci.References != nil {
			for _, r := range ci.References.ToSlice() {
				refs = append(refs, r)
			}
		}
		// tags (convert reference objects to strings)
		var tagNames []string
		if ts, ok := tags[hash]; ok {
			for _, r := range ts {
				tagNames = append(tagNames, r.Name().Short())
			}
		}
		// parents
		var parents []plumbing.Hash
		if ci != nil && ci.Commit != nil {
			for _, p := range ci.Commit.ParentHashes {
				parents = append(parents, p)
			}
		}
		svgCommits = append(svgCommits, SVGCommit{
			Hash: hash.String(),
			X:    pos[0],
			Y:    pos[1],
			Message: func() string {
				if ci != nil && ci.Commit != nil {
					return ci.Commit.Message
				}
				return ""
			}(),
			Refs:    refs,
			Tags:    tagNames,
			Parents: parents,
			Heads:   headNames,
		})
	}
	return svgCommits
}

// DrawRailway draws the railway graph to the provided svg canvas.
// children: map[parent] -> []child
func DrawRailway(
	canvas *svg.SVG,
	commits map[plumbing.Hash]*structs.CommitInfo,
	positions map[plumbing.Hash][2]int,
	heads map[plumbing.Hash][]*plumbing.Reference,
	tags map[plumbing.Hash][]*plumbing.Reference,
	children map[plumbing.Hash]mapset.Set[plumbing.Hash],
) {
	// compute max extents
	maxX, maxY := 0, 0
	for _, pos := range positions {
		if pos[0] > maxX {
			maxX = pos[0]
		}
		if pos[1] > maxY {
			maxY = pos[1]
		}
	}

	// Match python git-railway's presentation: flip Y so newest commits appear at the top.
	// (The stored lattice positions remain unchanged.)
	displayPositions := make(map[plumbing.Hash][2]int, len(positions))
	for h, pos := range positions {
		displayPositions[h] = [2]int{pos[0], maxY - pos[1]}
	}

	svgCommits := convertToSVGCommits(commits, displayPositions, heads, tags)

	width := paddingX*2 + (maxX+1)*stepX
	height := paddingY*2 + (maxY+1)*stepY

	canvas.Start(width, height)
	railway := NewSVGRailway(canvas)

	// sort commits by Y (so parents/children drawn before stops)
	sort.Slice(svgCommits, func(i, j int) bool {
		if svgCommits[i].Y == svgCommits[j].Y {
			return svgCommits[i].X < svgCommits[j].X
		}
		return svgCommits[i].Y < svgCommits[j].Y
	})

	// Precompute commits map for quick lookups (already provided as 'commits')
	// For each commit, draw rails from each parent -> commit
	for _, commit := range svgCommits {
		// Build a set of singleton refs for parents that have exactly one ref.
		singletons := mapset.NewSet[string]()
		for _, parentHash := range commit.Parents {
			if parentInfo, ok := commits[parentHash]; ok {
				// parentInfo.References assumed []string
				if parentInfo.References.Cardinality() == 1 {
					singletons.Add(parentInfo.References.ToSlice()[0])
				}
			}
		}

		// For each parent, decide colors for rail stack
		for _, parentHash := range commit.Parents {
			parentInfo, ok := commits[parentHash]
			if !ok {
				// parent's commit object not present (shallow) -> draw a simple gray rail
				railway.Rail(commit.X, commit.Y, commit.X, commit.Y-1, []color.RGBA{{219, 219, 219, 255}}, false)
				continue
			}

			// parent refs set and commit refs set
			parentRefsSet := mapset.NewSet[string]()
			for r := range parentInfo.References.Iter() {
				parentRefsSet.Add(r)
			}
			commitRefsSet := mapset.NewSet[string]()
			for _, r := range commit.Refs {
				commitRefsSet.Add(r)
			}

			// intersection and union
			common := parentRefsSet.Intersect(commitRefsSet)

			// target order: common refs first (stable ordering), then parent's-only, then commit-only
			var orderedRefs []string
			// add common (sorted for deterministic order)
			commonSlice := make([]string, 0, common.Cardinality())
			for r := range common.Iter() {
				commonSlice = append(commonSlice, r)
			}
			sort.Strings(commonSlice)
			orderedRefs = append(orderedRefs, commonSlice...)

			// parent's-only
			parentOnly := parentRefsSet.Difference(common)
			parentOnlySlice := make([]string, 0, parentOnly.Cardinality())
			for r := range parentOnly.Iter() {
				parentOnlySlice = append(parentOnlySlice, r)
			}
			sort.Strings(parentOnlySlice)
			orderedRefs = append(orderedRefs, parentOnlySlice...)

			// commit-only
			commitOnly := commitRefsSet.Difference(common)
			commitOnlySlice := make([]string, 0, commitOnly.Cardinality())
			for r := range commitOnly.Iter() {
				commitOnlySlice = append(commitOnlySlice, r)
			}
			sort.Strings(commitOnlySlice)
			orderedRefs = append(orderedRefs, commitOnlySlice...)

			// if still no refs (both sets empty) -> default gray
			if len(orderedRefs) == 0 {
				colors := []color.RGBA{{219, 219, 219, 255}}
				railway.Rail(commit.X, commit.Y, displayPositions[parentHash][0], displayPositions[parentHash][1], colors, true)
				continue
			}

			// construct colors slice (limit to maxColors)
			limit := len(orderedRefs)
			if limit > maxColors {
				limit = maxColors
			}
			colors := make([]color.RGBA, limit)
			for i := 0; i < limit; i++ {
				colors[i] = railway.refToColor(orderedRefs[i])
			}

			// draw rail from parent -> commit
			if ppos, ok := displayPositions[parentHash]; ok {
				middle := ppos[0] != commit.X // if diff in X, draw curved path
				railway.Rail(commit.X, commit.Y, ppos[0], ppos[1], colors, middle)
			} else {
				// fallback if parent position missing
				railway.Rail(commit.X, commit.Y, commit.X, commit.Y-1, colors, false)
			}
		}
	}

	// draw commit stops (after rails so they render on top)
	for _, commit := range svgCommits {
		railway.Stop(commit.X, commit.Y, color.RGBA{219, 219, 219, 255}, commit)
	}

	canvas.End()
}
