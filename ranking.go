package bytedp_memory

import (
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ==================== MMR (Maximal Marginal Relevance) ====================

// mmrItem represents an item for MMR processing
type mmrItem struct {
	id      string
	score   float64
	content string
	tokens  map[string]struct{}
}

// tokenize extracts alphanumeric tokens from text and normalizes to lowercase
func tokenize(text string) map[string]struct{} {
	re := regexp.MustCompile(`[a-z0-9_]+`)
	matches := re.FindAllString(strings.ToLower(text), -1)
	tokens := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		tokens[m] = struct{}{}
	}
	return tokens
}

// jaccardSimilarity computes Jaccard similarity between two token sets
// Returns a value in [0, 1] where 1 means identical sets
func jaccardSimilarity(setA, setB map[string]struct{}) float64 {
	if len(setA) == 0 && len(setB) == 0 {
		return 1
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}

	// Find intersection size
	intersection := 0
	for token := range setA {
		if _, exists := setB[token]; exists {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// maxSimilarityToSelected computes the maximum similarity between an item and all selected items
func maxSimilarityToSelected(item mmrItem, selectedItems []mmrItem) float64 {
	if len(selectedItems) == 0 {
		return 0
	}

	maxSim := 0.0
	for _, selected := range selectedItems {
		sim := jaccardSimilarity(item.tokens, selected.tokens)
		if sim > maxSim {
			maxSim = sim
		}
	}
	return maxSim
}

// computeMMRScore computes MMR score for a candidate item
// MMR = λ * relevance - (1-λ) * max_similarity_to_selected
func computeMMRScore(relevance, maxSimilarity, lambda float64) float64 {
	return lambda*relevance - (1-lambda)*maxSimilarity
}

// applyMMR re-ranks search results using Maximal Marginal Relevance
// MMR balances relevance with diversity by iteratively selecting results
// that maximize: λ * relevance - (1-λ) * max_similarity_to_selected
func applyMMR(results []*SearchResult, lambda float64) []*SearchResult {
	if len(results) == 0 {
		return results
	}

	// Clamp lambda to [0, 1]
	lambda = math.Max(0, math.Min(1, lambda))

	// Convert to MMR items
	items := make([]mmrItem, len(results))
	for i, r := range results {
		items[i] = mmrItem{
			id:      r.ID,
			score:   r.Score,
			content: r.Text,
			tokens:  tokenize(r.Text),
		}
	}

	var selected []mmrItem
	var remaining []mmrItem
	remaining = append(remaining, items...)

	// Greedy selection: pick the item with highest MMR score iteratively
	for len(remaining) > 0 {
		bestIdx := -1
		bestScore := math.Inf(-1)

		for i, item := range remaining {
			maxSim := maxSimilarityToSelected(item, selected)
			mmrScore := computeMMRScore(item.score, maxSim, lambda)
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}

		// Move best item from remaining to selected
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	// Rebuild results in MMR order
	resultMap := make(map[string]*SearchResult, len(results))
	for _, r := range results {
		resultMap[r.ID] = r
	}

	reordered := make([]*SearchResult, 0, len(selected))
	for _, item := range selected {
		if r, exists := resultMap[item.id]; exists {
			// Update score to reflect MMR ranking
			r.Score = item.score
			reordered = append(reordered, r)
		}
	}

	return reordered
}

// ApplyMMR re-ranks search results using MMR configuration
func ApplyMMR(results []*SearchResult, config MMRConfig) []*SearchResult {
	if !config.Enabled || len(results) < 2 {
		return results
	}
	return applyMMR(results, config.Lambda)
}

// ==================== Temporal decay ====================

// DayMs is the number of milliseconds in a day
const DayMs = 24 * 60 * 60 * 1000

// DatedMemoryPathRE matches daily memory files like memory/YYYY-MM-DD.md
var DatedMemoryPathRE = regexp.MustCompile(`^(?:.*/)?memory/(\d{4})-(\d{2})-(\d{2})\.md$`)

// toDecayLambda converts half-life days to decay constant lambda
// λ = ln(2) / half_life
func toDecayLambda(halfLifeDays float64) float64 {
	if halfLifeDays <= 0 || math.IsInf(halfLifeDays, 0) || math.IsNaN(halfLifeDays) {
		return 0
	}
	return math.Ln2 / halfLifeDays
}

// calculateTemporalDecayMultiplier computes the decay factor for a given age
// Uses exponential decay: multiplier = exp(-λ * age)
func calculateTemporalDecayMultiplier(ageInDays, halfLifeDays float64) float64 {
	lambda := toDecayLambda(halfLifeDays)
	clampedAge := math.Max(0, ageInDays)
	if lambda <= 0 || math.IsInf(clampedAge, 0) || math.IsNaN(clampedAge) {
		return 1
	}
	return math.Exp(-lambda * clampedAge)
}

// applyTemporalDecayToScore applies temporal decay to a score
func applyTemporalDecayToScore(score, ageInDays, halfLifeDays float64) float64 {
	return score * calculateTemporalDecayMultiplier(ageInDays, halfLifeDays)
}

// parseMemoryDateFromPath extracts date from memory file path (e.g., memory/2024-02-15.md)
func parseMemoryDateFromPath(filePath string) *time.Time {
	matches := DatedMemoryPathRE.FindStringSubmatch(filepath.ToSlash(filePath))
	if len(matches) < 4 {
		return nil
	}

	year, err1 := strconv.Atoi(matches[1])
	month, err2 := strconv.Atoi(matches[2])
	day, err3 := strconv.Atoi(matches[3])

	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}

	// Create UTC time for the date
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return &t
}

// isEvergreenMemoryPath checks if a memory path is for "evergreen" knowledge
// Evergreen memories don't decay: MEMORY.md, topic files in memory/ root
func isEvergreenMemoryPath(filePath string) bool {
	normalized := filepath.ToSlash(filePath)
	base := filepath.Base(normalized)

	// MEMORY.md at root is evergreen
	if base == "MEMORY.md" || base == "memory.md" {
		return true
	}

	// Files directly under memory/ (not dated) are evergreen
	if filepath.Dir(normalized) == "memory" && !DatedMemoryPathRE.MatchString(normalized) {
		return true
	}

	return false
}

// extractTimestamp extracts the timestamp from a memory entry
// Returns nil for evergreen memories (no decay)
func extractTimestamp(ve *VectorEmbedding) *time.Time {
	// Check metadata timestamp first
	if ve.Metadata.Timestamp != nil {
		return ve.Metadata.Timestamp
	}

	// Try to parse from file path
	if ve.Metadata.FilePath != "" {
		// Evergreen memories don't decay
		if ve.Source == MemorySourceLongTerm && isEvergreenMemoryPath(ve.Metadata.FilePath) {
			return nil
		}

		// Daily memories have date in path
		if parsed := parseMemoryDateFromPath(ve.Metadata.FilePath); parsed != nil {
			return parsed
		}
	}

	// Fall back to created_at
	return &ve.CreatedAt
}

// applyTemporalDecayToResults applies temporal decay to search results
func applyTemporalDecayToResults(results []*SearchResult, config TemporalDecayConfig, now time.Time) []*SearchResult {
	if !config.Enabled || len(results) == 0 {
		return results
	}

	halfLife := config.HalfLifeDays
	if halfLife <= 0 {
		halfLife = 30 // Default 30 days
	}

	for _, r := range results {
		timestamp := extractTimestamp(&r.VectorEmbedding)
		if timestamp == nil {
			// Evergreen memory - no decay
			r.AgeInDays = 0
			continue
		}

		ageMs := float64(now.Sub(*timestamp).Milliseconds())
		ageDays := ageMs / DayMs
		r.AgeInDays = ageDays

		// Apply decay
		r.Score = applyTemporalDecayToScore(r.Score, ageDays, halfLife)
	}

	// Re-sort by decayed score
	// Simple bubble sort for small slices (results are typically < 100)
	n := len(results)
	for i := 0; i < n-1; i++ {
		swapped := false
		for j := 0; j < n-i-1; j++ {
			if results[j].Score < results[j+1].Score {
				results[j], results[j+1] = results[j+1], results[j]
				swapped = true
			}
		}
		if !swapped {
			break
		}
	}

	return results
}

// ApplyTemporalDecay applies temporal decay to search results
func ApplyTemporalDecay(results []*SearchResult, config TemporalDecayConfig) []*SearchResult {
	return applyTemporalDecayToResults(results, config, time.Now())
}
