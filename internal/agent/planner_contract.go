package agent

type PlannerAction string

const (
	plannerActionOffTopicReject PlannerAction = "off_topic_reject"
	plannerActionSocialRefuse   PlannerAction = "social_refuse"
	plannerActionContinueTask   PlannerAction = "continue_task"
	plannerActionStartTask      PlannerAction = "start_task"
	plannerActionTaskMeta       PlannerAction = "task_meta"
	plannerActionCancelTask     PlannerAction = "cancel_task"
)

type PlannerDecision struct {
	Action         PlannerAction
	TaskType       string
	UserIntent     string
	Slots          map[string]string
	NeedsReplyOnly bool
	KeepTaskOpen   bool
	SwitchTask     bool
	Confidence     float64
	Reason         string
}
