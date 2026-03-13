package matcher

import (
	"os"
	"sort"
	"strings"

	"game-asset-pipeline-go/internal/model"
	"game-asset-pipeline-go/internal/util"
)

type ScoredCandidate struct {
	C model.AssetCandidate
	Score int
	Reason string
}

func BestMatch(game model.GameSpec, candidates []model.AssetCandidate) (best *model.AssetCandidate, score int, reason string) {
	targets := buildTargets(game)

	var scored []ScoredCandidate
	for _, c := range candidates {
		s, r := scoreCandidate(targets, c)
		scored = append(scored, ScoredCandidate{C: c, Score: s, Reason: r})
	}

	// Sort by score (highest first)
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) == 0 {
		return nil, 0, "no candidates"
	}
	if scored[0].Score <= 0 {
		return nil, scored[0].Score, scored[0].Reason
	}

	// If multiple candidates have the same best score (or within 2 points), prefer the one with larger area
	bestScore := scored[0].Score
	var bestCandidates []ScoredCandidate
	for _, sc := range scored {
		if sc.Score >= bestScore-2 && sc.Score <= bestScore+2 {
			bestCandidates = append(bestCandidates, sc)
		} else {
			break
		}
	}

	// Sort by area (largest first) among candidates with similar scores
	if len(bestCandidates) > 1 {
		sort.Slice(bestCandidates, func(i, j int) bool {
			areaI := getCandidateArea(bestCandidates[i].C)
			areaJ := getCandidateArea(bestCandidates[j].C)
			return areaI > areaJ
		})
	}

	b := bestCandidates[0]
	return &b.C, b.Score, b.Reason
}

func buildTargets(game model.GameSpec) []string {
	var t []string
	if game.GameName != "" { t = append(t, game.GameName) }
	if game.EnglishTitle != "" && game.EnglishTitle != game.GameName {
		t = append(t, game.EnglishTitle)
	}
	for _, a := range game.Aliases {
		if strings.TrimSpace(a) != "" {
			t = append(t, a)
		}
	}
	return t
}

func scoreCandidate(targets []string, c model.AssetCandidate) (int, string) {
	// Candidate text pool
	pool := strings.Join([]string{c.Title, c.Alt, c.FileName, c.URL}, " ")
	poolN := util.Normalize(pool)

	best := 0
	bestReason := "no match"
	for _, t := range targets {
		tn := util.Normalize(t)
		if tn == "" {
			continue
		}
		// exact match
		if poolN == tn {
			if 100 > best {
				best = 100
				bestReason = "exact normalize match"
			}
			continue
		}
		// contains
		if strings.Contains(poolN, tn) {
			if 85 > best {
				best = 85
				bestReason = "candidate contains target"
			}
		}
		// token overlap
		ts := util.Tokenize(tn)
		ps := util.Tokenize(poolN)
		ov := tokenOverlap(ts, ps)
		if ov > 0 {
			s := 40 + ov*10
			if s > best {
				best = s
				bestReason = "token overlap"
			}
		}
	}
	// slight bonus if filename contains any target tokens
	if best > 0 && c.FileName != "" {
		fn := util.Normalize(c.FileName)
		for _, t := range targets {
			tn := util.Normalize(t)
			if tn != "" && strings.Contains(fn, tn) {
				best += 5
				bestReason += " + filename bonus"
				break
			}
		}
	}
	if best > 100 {
		best = 100
	}
	return best, bestReason
}

func tokenOverlap(a, b []string) int {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	ov := 0
	for _, y := range b {
		if set[y] {
			ov++
		}
	}
	return ov
}

// getCandidateArea returns the area (Width*Height) of a candidate.
// If Width/Height are 0, fallback to file size if local path exists.
func getCandidateArea(c model.AssetCandidate) int64 {
	if c.Width > 0 && c.Height > 0 {
		return int64(c.Width) * int64(c.Height)
	}
	// Fallback to file size if local path exists
	if info, err := os.Stat(c.URL); err == nil {
		return info.Size()
	}
	return 0
}
