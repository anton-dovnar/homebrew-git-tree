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
	if c, exists := sr.colors[ref]; exists {
		return c
	}

	hash := md5.Sum([]byte(ref))
	h := float64(hash[0]) / 255.0
	s := 0.5 + (float64(hash[1])/255.0)*0.3 // 0.5-0.8 saturation
	l := 0.6 + (float64(hash[2])/255.0)*0.2 // 0.6-0.8 lightness

	c := hslToRGB(h, s, l)

	sr.colors[ref] = c
	return c
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

func (sr *SVGRailway) addS(path *string, dx, dy float64) {
	cp1x := 0.0
	cp1y := float64(stepY) * (1.0 / 5.0) * dy
	cp2x := -float64(stepX) * (1.0 / 4.0) * dx
	cp2y := float64(stepY) * (2.0 / 5.0) * dy
	end1x := -float64(stepX) * (1.0 / 2.0) * dx
	end1y := float64(stepY) * (1.0 / 2.0) * dy

	cp3x := -float64(stepX) * (1.0 / 4.0) * dx
	cp3y := float64(stepY) * (1.0 / 10.0) * dy
	cp4x := -float64(stepX) * (1.0 / 2.0) * dx
	cp4y := float64(stepY) * (3.0 / 10.0) * dy
	end2x := -float64(stepX) * (1.0 / 2.0) * dx
	end2y := float64(stepY) * (1.0 / 2.0) * dy

	*path += fmt.Sprintf("c %.1f %.1f %.1f %.1f %.1f %.1f ", cp1x, cp1y, cp2x, cp2y, end1x, end1y)
	*path += fmt.Sprintf("c %.1f %.1f %.1f %.1f %.1f %.1f ", cp3x, cp3y, cp4x, cp4y, end2x, end2y)
}

func (sr *SVGRailway) Rail(x, y, px, py int, colors []color.RGBA, middle bool) {
	if len(colors) == 0 {
		colors = []color.RGBA{{128, 128, 128, 255}} // "gray"
	}

	n := len(colors)
	w := float64(railW) / float64(n)
	dX := -float64(n-1) / 2 * w
	dx := x - px

	for i, c := range colors {
		ox := dX + float64(i)*w
		path := ""

		if middle {
			startX := paddingX + float64(x)*stepX + ox
			startY := paddingY + float64(y)*stepY
			path = fmt.Sprintf("M %.1f %d ", startX, int(startY))

			if dx != 0 {
				dl := float64(dx)
				dr := float64(dx)
				if int(dl)&1 == 0 {
					dl -= 1
					dr += 1
				}
				sr.addS(&path, dl/2, 1)
				path += fmt.Sprintf("V %d ", paddingY+(py-1)*stepY)
				sr.addS(&path, dr/2, 1)
			} else {
				sr.addS(&path, -0.5, 1)
				path += fmt.Sprintf("V %d ", paddingY+(py-1)*stepY)
				sr.addS(&path, 0.5, 1)
			}
		} else if dx != 0 {
			if dx > 0 {
				startX := paddingX + float64(x)*stepX + ox
				startY := paddingY + float64(y)*stepY
				path = fmt.Sprintf("M %.1f %d ", startX, int(startY))
				path += fmt.Sprintf("V %d ", paddingY+(py-1)*stepY)
				sr.addS(&path, float64(dx), 1)
			} else {
				startX := paddingX + float64(px)*stepX + ox
				startY := paddingY + float64(py)*stepY
				path = fmt.Sprintf("M %.1f %d ", startX, int(startY))
				path += fmt.Sprintf("V %d ", paddingY+(y+1)*stepY)
				sr.addS(&path, float64(-dx), -1)
			}
		} else {
			startX := paddingX + float64(x)*stepX + ox
			startY := paddingY + float64(y)*stepY
			path = fmt.Sprintf("M %.1f %d ", startX, int(startY))
			path += fmt.Sprintf("V %d", paddingY+py*stepY)
		}

		strokeWidth := w
		sr.Path(path, fmt.Sprintf(`fill="none" stroke="%s" stroke-width="%.1f"`, colorToHex(c), strokeWidth))
	}
}

func (sr *SVGRailway) Stop(x, y int, c color.RGBA, commit SVGCommit) {
	cx := paddingX + x*stepX
	cy := paddingY + y*stepY
	sr.Circle(cx, cy, stopR, fmt.Sprintf(`class="stop" fill="%s" id="%s"`, colorToHex(c), commit.Hash))
	sr.addLabels(x, y, commit)
}

func (sr *SVGRailway) addLabels(x, y int, commit SVGCommit) {
	hashX := 8
	ty := paddingY + y*stepY + 2

	labelX := paddingX + x*stepX + paddingY

	hashText := commit.Hash
	if len(commit.Hash) >= 7 {
		hashText = commit.Hash[:7]
	}
	sr.Text(hashX, ty, hashText,
		`fill="#c9bcbc" font-family="Ubuntu Mono" font-size="50%"`)

	refOffset := 0
	for _, ref := range commit.Heads {
		refColor := sr.refToColor(ref)
		sr.Writer.Write([]byte(fmt.Sprintf(`<text x="%d" y="%d"><tspan fill="%s" font-family="Ubuntu Mono" font-size="60%%" font-weight="bold">%s </tspan></text>`,
			labelX+refOffset, ty, colorToHex(refColor), ref)))
		refOffset += len(ref)*6 + 10
	}

	tagOffset := refOffset
	for _, tag := range commit.Tags {
		sr.Writer.Write([]byte(fmt.Sprintf(`<text x="%d" y="%d"><tspan fill="#dad682" font-family="Ubuntu Mono" font-size="60%%" font-weight="bold">🏷 %s </tspan></text>`,
			labelX+tagOffset, ty, tag)))
		tagOffset += len(tag)*6 + 20
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
			continue
		}
		var headNames []string
		if hs, ok := heads[hash]; ok {
			for _, r := range hs {
				headNames = append(headNames, r.Name().Short())
			}
		}
		var refs []string
		if ci != nil && ci.References != nil {
			for _, r := range ci.References.ToSlice() {
				refs = append(refs, r)
			}
		}
		var tagNames []string
		if ts, ok := tags[hash]; ok {
			for _, r := range ts {
				tagNames = append(tagNames, r.Name().Short())
			}
		}
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

func DrawRailway(
	canvas *svg.SVG,
	commits map[plumbing.Hash]*structs.CommitInfo,
	positions map[plumbing.Hash][2]int,
	heads map[plumbing.Hash][]*plumbing.Reference,
	tags map[plumbing.Hash][]*plumbing.Reference,
	children map[plumbing.Hash]mapset.Set[plumbing.Hash],
) {
	maxX, maxY := 0, 0
	for _, pos := range positions {
		if pos[0] > maxX {
			maxX = pos[0]
		}
		if pos[1] > maxY {
			maxY = pos[1]
		}
	}

	displayPositions := make(map[plumbing.Hash][2]int, len(positions))
	for h, pos := range positions {
		displayPositions[h] = [2]int{pos[0], maxY - pos[1]}
	}

	svgCommits := convertToSVGCommits(commits, displayPositions, heads, tags)

	hashStringToHash := make(map[string]plumbing.Hash)
	for hash := range commits {
		hashStringToHash[hash.String()] = hash
	}

	width := paddingX*2 + (maxX+1)*stepX
	height := paddingY*2 + (maxY+1)*stepY

	canvas.Startview(int(float64(width)*scale), int(float64(height)*scale), 0, 0, width, height)
	railway := NewSVGRailway(canvas)

	sort.Slice(svgCommits, func(i, j int) bool {
		if svgCommits[i].Y == svgCommits[j].Y {
			return svgCommits[i].X < svgCommits[j].X
		}
		return svgCommits[i].Y < svgCommits[j].Y
	})

	for _, commit := range svgCommits {
		singletons := mapset.NewSet[string]()
		for _, parentHash := range commit.Parents {
			if parentInfo, ok := commits[parentHash]; ok {
				if parentInfo.References.Cardinality() == 1 {
					singletons.Add(parentInfo.References.ToSlice()[0])
				}
			}
		}

		for _, parentHash := range commit.Parents {
			parentInfo, ok := commits[parentHash]
			if !ok {
				railway.Rail(commit.X, commit.Y, commit.X, commit.Y-1, []color.RGBA{{128, 128, 128, 255}}, false)
				continue
			}

			parentRefsSet := mapset.NewSet[string]()
			for r := range parentInfo.References.Iter() {
				parentRefsSet.Add(r)
			}
			commitRefsSet := mapset.NewSet[string]()
			for _, r := range commit.Refs {
				commitRefsSet.Add(r)
			}

			common := parentRefsSet.Intersect(commitRefsSet)

			var orderedRefs []string

			if commitRefsSet.Cardinality() > 1 && common.Cardinality() > 0 {
				commonSlice := make([]string, 0, common.Cardinality())
				for r := range common.Iter() {
					if parentRefsSet.Cardinality() == 1 || !singletons.Contains(r) {
						commonSlice = append(commonSlice, r)
					}
				}
				sort.Strings(commonSlice)
				orderedRefs = commonSlice
			} else {
				usedRefs := mapset.NewSet[string]()
				if childSet, ok := children[parentHash]; ok {
					for childHash := range childSet.Iter() {
						if childInfo, ok := commits[childHash]; ok && childInfo.References != nil {
							for r := range childInfo.References.Iter() {
								usedRefs.Add(r)
							}
						}
					}
				}

				var refsToUse mapset.Set[string]
				if common.Cardinality() > 0 || len(commit.Parents) <= 1 {
					refsToUse = commitRefsSet
				} else {
					refsToUse = parentRefsSet.Difference(usedRefs)
				}

				refsSlice := make([]string, 0, refsToUse.Cardinality())
				for r := range refsToUse.Iter() {
					refsSlice = append(refsSlice, r)
				}
				sort.Strings(refsSlice)
				orderedRefs = refsSlice
			}

		ppos, pposOk := displayPositions[parentHash]
		middle := false
		if pposOk {
			parentOrigPos, parentOk := positions[parentHash]
			commitHash, commitHashOk := hashStringToHash[commit.Hash]
			commitOrigPos, commitOk := positions[commitHash]
			if parentOk && commitOk && commitHashOk {
				pxOrig, pyOrig := parentOrigPos[0], parentOrigPos[1]
				cxOrig, cyOrig := commitOrigPos[0], commitOrigPos[1]
				intermediateX := pxOrig
				if pxOrig < cxOrig {
					intermediateX = cxOrig
				}
				pyFlipped := maxY - pyOrig
				cyFlipped := maxY - cyOrig
				for otherHash, otherOrigPos := range positions {
					if otherHash == commitHash || otherHash == parentHash {
						continue
					}
					rxOrig, ryOrig := otherOrigPos[0], otherOrigPos[1]
					ryFlipped := maxY - ryOrig
					if rxOrig == intermediateX && pyFlipped > ryFlipped && ryFlipped > cyFlipped {
						middle = true
						break
					}
				}
			}
		}

		if len(orderedRefs) == 0 {
			if commitRefsSet.Cardinality() > 0 && parentRefsSet.Cardinality() > 0 {
				refsSlice := make([]string, 0, commitRefsSet.Cardinality())
				for r := range commitRefsSet.Iter() {
					refsSlice = append(refsSlice, r)
				}
				sort.Strings(refsSlice)
				orderedRefs = refsSlice
			} else {
				colors := []color.RGBA{{128, 128, 128, 255}}
				if pposOk {
					railway.Rail(commit.X, commit.Y, ppos[0], ppos[1], colors, middle)
				} else {
					railway.Rail(commit.X, commit.Y, commit.X, commit.Y-1, colors, false)
				}
				continue
			}
		}

		limit := len(orderedRefs)
		if limit > maxColors {
			limit = maxColors
		}
		colors := make([]color.RGBA, limit)
		for i := 0; i < limit; i++ {
			colors[i] = railway.refToColor(orderedRefs[i])
		}

		if pposOk {
			railway.Rail(commit.X, commit.Y, ppos[0], ppos[1], colors, middle)
		} else {
			railway.Rail(commit.X, commit.Y, commit.X, commit.Y-1, colors, false)
		}
		}
	}

	for _, commit := range svgCommits {
		railway.Stop(commit.X, commit.Y, color.RGBA{219, 219, 219, 255}, commit)
	}

	canvas.End()
}
