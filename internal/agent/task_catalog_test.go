package agent

import "testing"

func TestTaskCatalogAllowsMigratedTaskTypesOnly(t *testing.T) {
	t.Parallel()

	catalog := newTaskCatalog(newTaskRuntime([]TaskHandler{
		newSubscribeTaskHandler(),
		newSubscriptionStatusTaskHandler(),
		newManualSignTaskHandler(),
	}))

	if !catalog.IsAllowed("subscribe_attendance_push") {
		t.Fatalf("subscribe_attendance_push should be allowlisted")
	}
	if !catalog.IsAllowed("query_subscription_status") {
		t.Fatalf("query_subscription_status should be allowlisted")
	}
	if !catalog.IsAllowed("sign_for_user") {
		t.Fatalf("sign_for_user should be allowlisted")
	}
	if catalog.IsAllowed("query_attendance_status") {
		t.Fatalf("query_attendance_status should not be allowlisted as a task")
	}
}
