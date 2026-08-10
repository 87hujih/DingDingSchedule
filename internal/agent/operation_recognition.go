package agent

type RecognitionSpec struct {
	Description      string
	Aliases          []string
	Examples         []string
	NegativeExamples []string
	RawSlots         []RawSlotSpec
	ContinueShapes   []ContinueShape
}

// RawSlotSpec defines the untrusted language-level input accepted for a
// trusted operation parameter. The resolver remains responsible for turning
// RawName into TargetParam; the compiler must never manufacture trusted IDs.
type RawSlotSpec struct {
	RawName     string
	TargetParam string
	Resolver    string
	Shape       string
	Required    bool
}

type ContinueShape struct {
	WorkflowType WorkflowType
	States       []WorkflowState
	Fields       []string
	Source       string
}

func rawSlotMapsTo(specs []RawSlotSpec, rawName, targetParam, resolver string) bool {
	for _, spec := range specs {
		if spec.RawName == rawName && spec.TargetParam == targetParam && spec.Resolver == resolver {
			return true
		}
	}
	return false
}

func rawSlotDeclared(specs []RawSlotSpec, rawName string) bool {
	_, ok := lookupRawSlotSpec(specs, rawName)
	return ok
}

func lookupRawSlotSpec(specs []RawSlotSpec, rawName string) (RawSlotSpec, bool) {
	for _, spec := range specs {
		if spec.RawName == rawName {
			return spec, true
		}
	}
	return RawSlotSpec{}, false
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
