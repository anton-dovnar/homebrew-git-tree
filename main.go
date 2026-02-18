package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"path/filepath"

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
			obj, err := repo.TagObject(ref.Hash())
			if err == nil {
				if commit, err := obj.Commit(); err == nil {
					toProcess.Add(commit.Hash)
					return nil
				}
			}
			toProcess.Add(ref.Hash()) // fallback for lightweight tag
		case all && name.IsRemote():
			toProcess.Add(ref.Hash())
		}
		return nil
	})

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

		if all && ref.Name().IsRemote() {
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
			obj, err := repo.TagObject(ref.Hash())
			if err == nil {
				if commit, err := obj.Commit(); err == nil {
					tags[commit.Hash] = append(tags[commit.Hash], ref)
					return nil
				}
			}
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

	ctsort := func() []commitPair {
		sortedCommits := make([]commitPair, 0, len(commits))
		for h, ci := range commits {
			if ci != nil && ci.Commit != nil {
				sortedCommits = append(sortedCommits, commitPair{Hash: h, Ci: ci})
			}
		}
		sort.Slice(sortedCommits, func(i, j int) bool {
			return sortedCommits[i].Ci.Commit.Committer.When.Before(sortedCommits[j].Ci.Commit.Committer.When)
		})

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
					result = append(result, sortedCommits...)
					sortedCommits = sortedCommits[:0]
					break
				}
				h := sortedCommits[i].Hash
				if parents[h].Cardinality() == 0 {
					c := sortedCommits[i]
					sortedCommits = append(sortedCommits[:i], sortedCommits[i+1:]...)
					result = append(result, c)
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

	isHeadRef := func(r *plumbing.Reference) bool {
		if r == nil {
			return false
		}
		name := r.Name().String()
		return len(name) >= len("refs/heads/") && name[:len("refs/heads/")] == "refs/heads/"
	}

	buildHeadChildren := func() (map[plumbing.Hash]mapset.Set[plumbing.Hash], map[plumbing.Hash]mapset.Set[plumbing.Hash]) {
		headChildren := make(map[plumbing.Hash]mapset.Set[plumbing.Hash])
		for h, refSlice := range heads {
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

	sortedCommits := ctsort()
	if len(sortedCommits) == 0 {
		return nil
	}

	first := sortedCommits[0]
	h0 := first.Hash
	initialRefs := first.Ci.References
	headChildren, childrenHead := buildHeadChildren()
	refsLevels := make(map[string]int)
	for ref := range initialRefs.Iter() {
		refsLevels[ref] = 0
	}
	seenHeads := mapset.NewSet[plumbing.Hash]()

	locations := make(map[plumbing.Hash][2]int, len(sortedCommits))
	locations[h0] = [2]int{0, 0}

	for i := 0; i < len(sortedCommits)-1; i++ {
		curPair := sortedCommits[i+1]
		h := curPair.Hash
		ci := curPair.Ci
		c := ci.Commit
		refs := ci.References

		x := -1

		activeRefs := mapset.NewSet[string]()
		for r := range refsLevels {
			activeRefs.Add(r)
		}

		if refs == nil || refs.Cardinality() == 0 {
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
					x = gap(refsLevels, false)
				}
			} else {
				x = gap(refsLevels, false)
			}

		} else if refs.Intersect(activeRefs).Cardinality() == 0 {
			x = gap(refsLevels, true)

		} else {
			px := make(map[plumbing.Hash]int)
			currentRefs := refs.Intersect(activeRefs) // current tracked refs on this commit
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

				if parentTracked.IsSubset(currentRefs) {
					if pos, ok := locations[p]; ok {
						xForParent = pos[0]
					}
				} else {
					diverged := false
					for _, lr := range levelRefs {
						curAtLevel := lr.Intersect(currentRefs)
						if curAtLevel.IsSubset(parentTracked) && !parentTracked.IsSubset(curAtLevel) {
							diverged = true
							break
						}
					}

					if diverged {
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
						if pos, ok := locations[p]; ok {
							xForParent = pos[0]
						}
					} else {
						reuseTracked := false
						for _, lr := range levelRefs {
							curAtLevel := lr.Intersect(currentRefs)
							if curAtLevel.IsSubset(currentRefs) && currentRefs.IsSubset(curAtLevel) {
								reuseTracked = true
								break
							}
						}
						if reuseTracked {
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
				x = gap(refsLevels, true)
			}
		}

		if x < 0 {
			x = 0
		}

		locations[h] = [2]int{x, len(locations)}

		for r := range refs.Iter() {
			refsLevels[r] = x
		}

		if _, ok := heads[h]; ok {
			seenHeads.Add(h)
		} else if ch, ok := childrenHead[h]; ok {
			for head := range ch.Iter() {
				if set, ok2 := headChildren[head]; ok2 {
					set.Remove(h)
				}
			}
		}

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

func getGitHubSlug(repo *git.Repository) string {
	remotes, err := repo.Remotes()
	if err != nil {
		return ""
	}

	for _, remote := range remotes {
		for _, url := range remote.Config().URLs {
			if strings.Contains(url, "github.com") {
				url = strings.TrimSuffix(url, ".git")
				if idx := strings.Index(url, "github.com/"); idx >= 0 {
					slug := url[idx+len("github.com/"):]
					if strings.HasPrefix(slug, ":") {
						slug = slug[1:]
					}
					return slug
				}
			}
		}
	}
	return ""
}

func main() {
	repoPath := flag.String("path", ".", "Path to Git repository (any subdirectory is OK)")
	all := flag.Bool("all", false, "Include remote refs")
	locationsOut := flag.String("locations", "locations.json", "Write computed lattice positions JSON to this path")
	noSVG := flag.Bool("no-svg", false, "Do not emit SVG to stdout")
	htmlOut := flag.String("html", "", "Generate HTML output file (instead of SVG to stdout)")
	htmlOnly := flag.Bool("html-only", false, "Skip SVG stdout output when generating HTML")
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

	if *htmlOut != "" {
		ghSlug := getGitHubSlug(repo)
		commitData := view.GenerateCommitData(commits, ghSlug)

		svgString, err := view.GenerateSVGString(commits, positions, heads, tags, children)
		if err != nil {
			log.Fatalf("Failed to generate SVG: %v", err)
		}

		title := *repoPath
		if title == "." {
			wd, err := os.Getwd()
			if err == nil {
				title = wd
			}
		}
		title = strings.TrimSuffix(title, "/")
		if idx := strings.LastIndex(title, "/"); idx >= 0 {
			title = title[idx+1:]
		}

		htmlFile, err := os.Create(*htmlOut)
		if err != nil {
			log.Fatalf("Failed to create HTML file %s: %v", *htmlOut, err)
		}
		defer htmlFile.Close()

		if err := view.WriteHTML(htmlFile, svgString, commitData, title); err != nil {
			log.Fatalf("Failed to write HTML: %v", err)
		}

		absPath, _ := filepath.Abs(*htmlOut)
		log.Printf("✨ HTML generated: file://%s", absPath)
	}

	if !*noSVG && (*htmlOut == "" || !*htmlOnly) {
		canvas := svg.New(os.Stdout)
		view.DrawRailway(canvas, commits, positions, heads, tags, children)
	}
}
