package agent

func workflowPrimaryOperationName(workflowType WorkflowType) string {
	if manifest, ok := lookupWorkflowPrimaryOperation(workflowType); ok {
		return manifest.Name
	}
	return ""
}

func workflowAuxiliaryOperationName(workflowType WorkflowType, executorBinding string) string {
	primary, ok := lookupWorkflowPrimaryOperation(workflowType)
	if !ok || primary.Workflow == nil {
		return ""
	}
	for _, operation := range primary.Workflow.AuxiliaryOperations {
		manifest, ok := lookupOperation(operation)
		if !ok {
			continue
		}
		if manifest.Executor.Name == executorBinding {
			return manifest.Name
		}
	}
	return ""
}

func lookupWorkflowPrimaryOperation(workflowType WorkflowType) (OperationManifest, bool) {
	for _, manifest := range operationManifests() {
		if manifest.Workflow != nil && manifest.Workflow.Type == workflowType && manifest.Workflow.Mode == WorkflowModeMultiTurn {
			return manifest, true
		}
	}
	return OperationManifest{}, false
}
