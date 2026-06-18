package agent

// legacy-only: plan runs the legacy planner used outside protocol_live.
func plan(input PlanInput) PlanDecision {
	knowledgeStrength := classifyKnowledgeStrength(input.Retrieval)

	if input.ConversationEvent.Event == eventTaskFollowUp && input.ActiveTask != nil {
		return PlanDecision{
			Kind:              planKindContinueTask,
			ActiveTask:        cloneActiveTask(input.ActiveTask),
			KnowledgeStrength: knowledgeStrength,
		}
	}

	if input.TaskCandidate != nil {
		if input.TaskCandidate.Status == taskStatusReady {
			return PlanDecision{
				Kind:              planKindTool,
				ActiveTask:        cloneActiveTask(input.TaskCandidate),
				KnowledgeStrength: knowledgeStrength,
			}
		}
		return PlanDecision{
			Kind:              planKindClarify,
			ActiveTask:        cloneActiveTask(input.TaskCandidate),
			ClarifyReason:     "missing_slots",
			KnowledgeStrength: knowledgeStrength,
		}
	}

	if knowledgeStrength == knowledgeStrengthStrong {
		if input.HasLiveSignal && input.HasRuleSignal {
			return PlanDecision{
				Kind:              planKindMixed,
				KnowledgeStrength: knowledgeStrength,
			}
		}
		if input.HasLiveSignal {
			return PlanDecision{
				Kind:              planKindTool,
				KnowledgeStrength: knowledgeStrength,
			}
		}
		return PlanDecision{
			Kind:              planKindRAG,
			KnowledgeStrength: knowledgeStrength,
		}
	}

	if input.HasActionIntent || input.HasLiveSignal {
		return PlanDecision{
			Kind:              planKindTool,
			KnowledgeStrength: knowledgeStrength,
		}
	}

	return PlanDecision{
		Kind:              planKindClarify,
		ClarifyReason:     "weak_domain_match",
		KnowledgeStrength: knowledgeStrength,
	}
}
