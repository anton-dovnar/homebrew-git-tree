package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/anton-dovnar/git-tree/structs"
	"github.com/anton-dovnar/git-tree/view"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	svg "github.com/ajstarks/svgo"

	mapset "github.com/deckarep/golang-set/v2"
)

func collectCommits(repoPath string, repo *git.Repository, all bool) (
	map[plumbing.Hash]*structs.CommitInfo,
	map[plumbing.Hash]mapset.Set[plumbing.Hash],
) {
	commits := make(map[plumbing.Hash]*structs.CommitInfo)
	children := make(map[plumbing.Hash]mapset.Set[plumbing.Hash])
	toProcess := mapset.NewSet[plumbing.Hash]()

	// Collect initial refs (local heads and tags)
	refIter, err := repo.References()
	if err != nil {
		log.Printf("Error reading references: %v", err)
		return nil, nil
	}
	defer refIter.Close()

	refIter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name()
		switch {
		case name.IsBranch():
			toProcess.Add(ref.Hash())
		case name.IsTag():
			// Resolve annotated or lightweight tag
			obj, err := repo.TagObject(ref.Hash())
			if err == nil {
				if commit, err := obj.Commit(); err == nil {
					toProcess.Add(commit.Hash)
					return nil
				}
			}
			toProcess.Add(ref.Hash()) // fallback for lightweight tag
		case all && name.IsRemote():
			// If all=True, include remote refs
			toProcess.Add(ref.Hash())
		}
		return nil
	})

	// Iteratively walk the commit graph (avoid recursion)
	for toProcess.Cardinality() > 0 {
		current, ok := toProcess.Pop()
		if !ok {
			continue
		}
		if _, exists := commits[current]; exists {
			continue
		}

		commit, err := repo.CommitObject(current)
		if err != nil {
			continue
		}

		commits[current] = &structs.CommitInfo{
			Commit:     commit,
			References: mapset.NewSet[string](),
		}

		for _, parent := range commit.ParentHashes {
			if _, ok := children[parent]; !ok {
				children[parent] = mapset.NewSet[plumbing.Hash]()
			}
			children[parent].Add(commit.Hash)
			toProcess.Add(parent)
		}
	}

	// Associate reflog references:
	// label commits based on reflog entries for local heads; and, when all=true,
	// label untracked remote refs (excluding */HEAD).
	gitDir, err := structs.ResolveGitDir(repoPath)
	if err != nil {
		log.Printf("Could not resolve git dir for reflogs (%s): %v", repoPath, err)
		return commits, children
	}

	trackedRemotes := map[string]struct{}{}
	if all {
		if m, err := structs.TrackedRemoteRefs(gitDir); err == nil {
			trackedRemotes = m
		}
	}

	refIter2, err := repo.References()
	if err != nil {
		return commits, children
	}
	defer refIter2.Close()

	refIter2.ForEach(func(ref *plumbing.Reference) error {
		refName := ref.Name().String()

		// local branch heads
		if ref.Name().IsBranch() {
			hashes, err := structs.ReadReflogNewHashes(gitDir, refName)
			if err != nil {
				return nil
			}
			for _, h := range hashes {
				if info, ok := commits[h]; ok {
					info.References.Add(refName)
				}
			}
			return nil
		}

		// remote refs (only when all=true)
		if all && ref.Name().IsRemote() {
			// Ignore remote/HEAD and tracked remote refs
			if strings.HasSuffix(refName, "/HEAD") {
				return nil
			}
			if _, ok := trackedRemotes[refName]; ok {
				return nil
			}

			hashes, err := structs.ReadReflogNewHashes(gitDir, refName)
			if err != nil {
				return nil
			}
			for _, h := range hashes {
				if info, ok := commits[h]; ok {
					info.References.Add(refName)
				}
			}
		}
		return nil
	})

	return commits, children
}

func getRefs(repo *git.Repository, all bool) (
	map[plumbing.Hash][]*plumbing.Reference,
	map[plumbing.Hash][]*plumbing.Reference,
) {
	heads := make(map[plumbing.Hash][]*plumbing.Reference)
	tags := make(map[plumbing.Hash][]*plumbing.Reference)

	refIter, err := repo.References()
	if err != nil {
		return nil, nil
	}
	defer refIter.Close()

	refIter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name()
		switch {
		case name.IsBranch():
			hash := ref.Hash()
			heads[hash] = append(heads[hash], ref)

		case name.IsTag():
			// For tags, try resolving annotated tags to commits
			obj, err := repo.TagObject(ref.Hash())
			if err == nil {
				if commit, err := obj.Commit(); err == nil {
					tags[commit.Hash] = append(tags[commit.Hash], ref)
					return nil
				}
			}
			// Lightweight tag fallback (direct commit)
			tags[ref.Hash()] = append(tags[ref.Hash()], ref)

		case all && name.IsRemote():
			hash := ref.Hash()
			heads[hash] = append(heads[hash], ref)
		}
		return nil
	})

	return heads, tags
}

func arrangeCommits(
	commits map[plumbing.Hash]*structs.CommitInfo,
	heads map[plumbing.Hash][]*plumbing.Reference,
	children map[plumbing.Hash]mapset.Set[plumbing.Hash],
) map[plumbing.Hash][2]int {

	type commitPair struct {
		Hash plumbing.Hash
		Ci   *structs.CommitInfo
	}

	// --- Chrono-topological sort (ctsort) ---
	ctsort := func() []commitPair {
		// sorted by committed_date
		sortedCommits := make([]commitPair, 0, len(commits))
		for h, ci := range commits {
			if ci != nil && ci.Commit != nil {
				sortedCommits = append(sortedCommits, commitPair{Hash: h, Ci: ci})
			}
		}
		sort.Slice(sortedCommits, func(i, j int) bool {
			return sortedCommits[i].Ci.Commit.Committer.When.Before(sortedCommits[j].Ci.Commit.Committer.When)
		})

		// parents: map[hash] -> set of parent hashes
		parents := make(map[plumbing.Hash]mapset.Set[plumbing.Hash], len(commits))
		for h, ci := range commits {
			ps := mapset.NewSet[plumbing.Hash]()
			if ci != nil && ci.Commit != nil {
				for _, p := range ci.Commit.ParentHashes {
					ps.Add(p)
				}
			}
			parents[h] = ps
		}

		result := make([]commitPair, 0, len(sortedCommits))
		for len(sortedCommits) > 0 {
			i := 0
			for {
				if i >= len(sortedCommits) {
					// If no parent-free node found (shouldn't happen for a DAG), append remainder to result
					result = append(result, sortedCommits...)
					sortedCommits = sortedCommits[:0]
					break
				}
				h := sortedCommits[i].Hash
				if parents[h].Cardinality() == 0 {
					// pop i
					c := sortedCommits[i]
					sortedCommits = append(sortedCommits[:i], sortedCommits[i+1:]...)
					result = append(result, c)
					// remove h from parents of its children
					if cs, ok := children[h]; ok {
						for child := range cs.Iter() {
							if ps, ok := parents[child]; ok {
								ps.Remove(h)
							}
						}
					}
					break
				}
				i++
			}
		}
		return result
	}

	// --- Helper: determine if a ref is a "head" (branch head) ---
	isHeadRef := func(r *plumbing.Reference) bool {
		if r == nil {
			return false
		}
		name := r.Name().String()
		// Approximate by branch heads.
		return len(name) >= len("refs/heads/") && name[:len("refs/heads/")] == "refs/heads/"
	}

	// --- Build head_children and children_head ---
	buildHeadChildren := func() (map[plumbing.Hash]mapset.Set[plumbing.Hash], map[plumbing.Hash]mapset.Set[plumbing.Hash]) {
		headChildren := make(map[plumbing.Hash]mapset.Set[plumbing.Hash])
		for h, refSlice := range heads {
			// include only heads where there is at least one Head reference
			hasHead := false
			for _, r := range refSlice {
				if isHeadRef(r) {
					hasHead = true
					break
				}
			}
			if hasHead {
				cs := mapset.NewSet[plumbing.Hash]()
				if ch, ok := children[h]; ok {
					for c := range ch.Iter() {
						cs.Add(c)
					}
				}
				headChildren[h] = cs
			}
		}

		childrenHead := make(map[plumbing.Hash]mapset.Set[plumbing.Hash])
		for head, cs := range headChildren {
			for c := range cs.Iter() {
				set := childrenHead[c]
				if set == nil {
					set = mapset.NewSet[plumbing.Hash]()
					childrenHead[c] = set
				}
				set.Add(head)
			}
		}
		return headChildren, childrenHead
	}

	// --- gap(refs=True/False) ---
	// Tracked levels are the values of refsLevels (map[string]int)
	gap := func(refsLevels map[string]int, refs bool) int {
		if len(refsLevels) == 0 {
			if refs {
				return 0
			}
			return 1
		}
		levelsSet := mapset.NewSet[int]()
		for _, l := range refsLevels {
			levelsSet.Add(l)
		}
		levels := make([]int, 0, levelsSet.Cardinality())
		for l := range levelsSet.Iter() {
			levels = append(levels, l)
		}
		sort.Ints(levels)
		for i := 0; i < len(levels)-1; i++ {
			if levels[i+1]-levels[i] > 1 {
				return levels[i] + 1
			}
		}
		return levels[len(levels)-1] + 1
	}

	// --- Execute ctsort ---
	sortedCommits := ctsort()
	if len(sortedCommits) == 0 {
		return nil
	}

	// Initial commit
	first := sortedCommits[0]
	h0 := first.Hash
	initialRefs := first.Ci.References

	// Map head commits with their children and reverse map
	headChildren, childrenHead := buildHeadChildren()

	// Track branch heights/levels
	refsLevels := make(map[string]int)
	for ref := range initialRefs.Iter() {
		refsLevels[ref] = 0
	}
	seenHeads := mapset.NewSet[plumbing.Hash]()

	// Locations map: commit -> (x, y)
	locations := make(map[plumbing.Hash][2]int, len(sortedCommits))
	locations[h0] = [2]int{0, 0}

	// For remaining commits
	for i := 0; i < len(sortedCommits)-1; i++ {
		curPair := sortedCommits[i+1]
		h := curPair.Hash
		ci := curPair.Ci
		c := ci.Commit
		refs := ci.References

		x := -1

		// active refs are keys of refsLevels
		activeRefs := mapset.NewSet[string]()
		for r := range refsLevels {
			activeRefs.Add(r)
		}

		// Case 1: commit has no refs
		if refs == nil || refs.Cardinality() == 0 {
			// position of the lowest parent
			type pxPair struct {
				parent plumbing.Hash
				x      int
			}
			parentPositions := make([]pxPair, 0, len(c.ParentHashes))
			for _, p := range c.ParentHashes {
				if pos, ok := locations[p]; ok {
					parentPositions = append(parentPositions, pxPair{parent: p, x: pos[0]})
				}
			}
			sort.Slice(parentPositions, func(a, b int) bool { return parentPositions[a].x < parentPositions[b].x })

			if len(parentPositions) > 0 {
				p := parentPositions[0].parent
				x = parentPositions[0].x

				// future_children = children[p] ∩ {h for h, _ in sorted_commits[i+2:]}
				futureChildren := mapset.NewSet[plumbing.Hash]()
				if cs, ok := children[p]; ok {
					remaining := mapset.NewSet[plumbing.Hash]()
					for k := i + 2; k < len(sortedCommits); k++ {
						remaining.Add(sortedCommits[k].Hash)
					}
					for child := range cs.Iter() {
						if remaining.Contains(child) {
							futureChildren.Add(child)
						}
					}
				}
				if futureChildren.Cardinality() > 0 {
					// use gap(refs=False)
					x = gap(refsLevels, false)
				}
			} else {
				// No parents known: place at a gap for non-ref case
				x = gap(refsLevels, false)
			}

		} else if refs.Intersect(activeRefs).Cardinality() == 0 {
			// Case 2: commit has new refs only
			x = gap(refsLevels, true)

		} else {
			// Case 3: commit has tracked refs
			px := make(map[plumbing.Hash]int)
			// m = max level value
			// We'll keep logic parity without relying on m.

			currentRefs := refs.Intersect(activeRefs) // current tracked refs on this commit

			// Precompute reverse: level -> set of refs currently tracked at that level
			levelRefs := make(map[int]mapset.Set[string])
			for r, lvl := range refsLevels {
				rs := levelRefs[lvl]
				if rs == nil {
					rs = mapset.NewSet[string]()
					levelRefs[lvl] = rs
				}
				rs.Add(r)
			}

			for _, p := range c.ParentHashes {
				parentInfo, ok := commits[p]
				if !ok || parentInfo == nil {
					continue
				}
				parentRefs := parentInfo.References
				parentTracked := mapset.NewSet[string]()
				if parentRefs != nil {
					parentTracked = parentRefs.Intersect(activeRefs)
				}

				xForParent := -1

				// If current_refs >= (parent_refs & active_refs) then reuse parent level.
				// Set-wise: parentTracked ⊆ currentRefs.
				if parentTracked.IsSubset(currentRefs) {
					if pos, ok := locations[p]; ok {
						xForParent = pos[0]
					}
				} else {
					// elif any(levelRefs[i] ∩ current_refs) < (parent_refs ∩ active_refs) for i in levels:
					// meaning: at each tracked level, the set of current refs present at that level
					// is a proper subset of parent's tracked refs.
					diverged := false
					for _, lr := range levelRefs {
						curAtLevel := lr.Intersect(currentRefs)
						// proper subset: curAtLevel < parentTracked
						if curAtLevel.IsSubset(parentTracked) && !parentTracked.IsSubset(curAtLevel) {
							diverged = true
							break
						}
					}

					if diverged {
						// try lowest ref level among current refs
						minX := -1
						for r := range currentRefs.Iter() {
							if lvl, ok := refsLevels[r]; ok {
								if minX == -1 || lvl < minX {
									minX = lvl
								}
							}
						}
						if minX == -1 {
							minX = gap(refsLevels, true)
						}
						xForParent = minX

						// if x == parent level and parent has multiple children, need a new level
						if pos, ok := locations[p]; ok {
							if xForParent == pos[0] {
								childCount := 0
								if cs, ok := children[p]; ok {
									childCount = cs.Cardinality()
								}
								if childCount != 1 {
									xForParent = gap(refsLevels, true)
								}
							}
						}
					} else if parentTracked.Cardinality() == 0 {
						// parent has no refs: use parent level
						if pos, ok := locations[p]; ok {
							xForParent = pos[0]
						}
					} else {
						// elif any(levelRefs[i] ∩ current_refs == current_refs) for i in levels:
						reuseTracked := false
						for _, lr := range levelRefs {
							curAtLevel := lr.Intersect(currentRefs)
							if curAtLevel.IsSubset(currentRefs) && currentRefs.IsSubset(curAtLevel) {
								reuseTracked = true
								break
							}
						}
						if reuseTracked {
							// use the level of any one current ref
							for r := range currentRefs.Iter() {
								if lvl, ok := refsLevels[r]; ok {
									xForParent = lvl
									break
								}
							}
						} else {
							xForParent = gap(refsLevels, true)
						}
					}
				}

				if xForParent < 0 {
					xForParent = gap(refsLevels, true)
				}

				px[p] = xForParent
			}

			// Choose minimum x across parents
			if len(px) > 0 {
				min := -1
				for _, v := range px {
					if v >= 0 && (min == -1 || v < min) {
						min = v
					}
				}
				if min != -1 {
					x = min
				} else {
					x = gap(refsLevels, true)
				}
			} else {
				// No valid parent info; place at a gap
				x = gap(refsLevels, true)
			}
		}

		if x < 0 {
			x = 0
		}

		locations[h] = [2]int{x, len(locations)}

		// Update levels of tracked refs
		for r := range refs.Iter() {
			refsLevels[r] = x
		}

		// Head/children bookkeeping
		if _, ok := heads[h]; ok {
			// Mark the head for untracking
			seenHeads.Add(h)
		} else if ch, ok := childrenHead[h]; ok {
			// Remove the child from the set of children of all its parent heads
			for head := range ch.Iter() {
				if set, ok2 := headChildren[head]; ok2 {
					set.Remove(h)
				}
			}
		}

		// Untrack seen heads: remove their refs from refsLevels
		for _, head := range seenHeads.ToSlice() {
			seenHeads.Remove(head) // Head in set(seen_heads): seen_heads.remove(head)
			if refSlice, ok := heads[head]; ok {
				for _, r := range refSlice {
					if r == nil {
						continue
					}
					name := r.Name().String()
					delete(refsLevels, name)
				}
			}
		}
	}

	return locations
}

func printLocations(locations map[plumbing.Hash][2]int) {
	for hash, pos := range locations {
		fmt.Printf("'%s': [%d,%d]\n", hash.String(), pos[0], pos[1])
	}
}

func saveLocationsToFile(locations map[plumbing.Hash][2]int, filename string) error {
	stringMap := make(map[string][2]int)
	for hash, pos := range locations {
		stringMap[hash.String()] = pos
	}

	jsonData, err := json.MarshalIndent(stringMap, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, jsonData, 0644)
}

func main() {
	repoPath := flag.String("path", ".", "Path to Git repository (any subdirectory is OK)")
	all := flag.Bool("all", false, "Include remote refs")
	locationsOut := flag.String("locations", "locations.json", "Write computed lattice positions JSON to this path")
	noSVG := flag.Bool("no-svg", false, "Do not emit SVG to stdout")
	flag.Parse()

	repo, err := git.PlainOpenWithOptions(*repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		log.Fatal(err)
	}

	commits, children := collectCommits(*repoPath, repo, *all)
	log.Printf("Collected %d commits", len(commits))
	log.Printf("Collected %d child relationships", len(children))

	heads, tags := getRefs(repo, *all)
	log.Printf("Collected %d heads", len(heads))
	log.Printf("Collected %d tags", len(tags))

	positions := arrangeCommits(commits, heads, children)
	log.Printf("Arranged %d commits", len(positions))
	if err := saveLocationsToFile(positions, *locationsOut); err != nil {
		log.Printf("Could not save locations to %s: %v", *locationsOut, err)
	}

	if !*noSVG {
		canvas := svg.New(os.Stdout)
		view.DrawRailway(canvas, commits, positions, heads, tags, children)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
