package agent

import "strings"

type RecognitionSpec struct {
	Aliases          []string
	Examples         []string
	NegativeExamples []string
	SlotHints        []SlotHint
	ContinueShapes   []ContinueShape
}

type SlotHint struct {
	Field string
	Shape string
}

type ContinueShape struct {
	WorkflowType WorkflowType
	States       []WorkflowState
	Fields       []string
	Source       string
}

func recognitionContainsAlias(recognition RecognitionSpec, alias string) bool {
	target := normalizeQuery(alias)
	for _, candidate := range recognition.Aliases {
		if normalizeQuery(candidate) == target {
			return true
		}
	}
	return false
}

func recognitionAliasMatches(message string, alias string) bool {
	normalizedMessage := normalizeQuery(message)
	normalizedAlias := normalizeQuery(alias)
	if normalizedMessage == "" || normalizedAlias == "" {
		return false
	}
	if normalizedMessage == normalizedAlias {
		return true
	}
	return strings.Contains(normalizedMessage, normalizedAlias)
}

func recognitionAliasConfidence(message string, alias string) float64 {
	normalizedMessage := normalizeQuery(message)
	normalizedAlias := normalizeQuery(alias)
	if normalizedMessage == normalizedAlias {
		return 1
	}
	score := 0.8 + float64(len([]rune(normalizedAlias)))/100
	if score > 0.99 {
		return 0.99
	}
	return score
}
