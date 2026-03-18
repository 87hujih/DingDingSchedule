# Agent Query Tools Dept Name Support Design

## Background

The agent already exposes department-filtered attendance and analytics queries, but the public query tools only accept `dept_id`.
In practice, the LLM often receives natural language department names from users and cannot reliably translate them into IDs because `list_departments` is only available to admins.
This causes the agent to fall back to unfiltered queries and return whole-tenant attendance results.

## Goal

Allow all agent query tools that currently support department filtering to accept `dept_name` directly, while keeping `dept_id` compatible.

The affected tools are:

- `query_attendance_status`
- `generate_attendance_text`
- `query_attendance_stats`
- `query_user_cross`

## Non-Goals

- Do not change service or repository filtering behavior.
- Do not make `list_departments` public.
- Do not introduce fuzzy or semantic department matching.
- Do not change multi-department subscription behavior in admin tools.

## Recommended Approach

Keep the current downstream API shape based on a single `dept_id`, and add a tool-layer resolver that converts `dept_name` into a single department ID before dispatching to services.

Each affected tool will accept both:

- `dept_name` as the preferred field
- `dept_id` as a backward-compatible fallback

Resolution rules:

1. If `dept_name` is non-empty after trimming, resolve by exact department name match.
2. If `dept_name` resolves successfully, use that department's ID and ignore `dept_id`.
3. If `dept_name` is empty and `dept_id` is provided, keep the existing behavior.
4. If both are empty, keep the existing "no department filter" behavior.

## Tool-Layer Design

### Shared Resolver

Add a shared helper under `internal/agent/tools` responsible for converting tool input into a final department ID.

The helper will depend on `DeptPort` and return:

- resolved department ID
- whether a department filter should be applied
- a user-facing JSON error payload for expected validation failures

Expected validation failures handled in the helper:

- unknown department name
- duplicate department names within the current tenant

These should return business-style JSON results instead of bubbling a Go error, so the agent can respond with a clear explanation rather than a generic "tool execution failed".

### Affected Registration Functions

Update these registrations to receive `DeptPort`:

- `RegisterAttendanceTools`
- `RegisterAnalyticsTools`

`agent.NewAgent` will pass the existing `deps.Dept` into both registrations.

## Parameter Schema Changes

For all 4 affected tools:

- add `dept_name` to the JSON schema
- keep `dept_id`
- update descriptions to say that `dept_name` is preferred and `dept_id` is retained for compatibility

When both are present, tool behavior will prefer `dept_name`.

## Error Handling

Department name matching will be exact after `strings.TrimSpace`.

Rules:

- blank `dept_name` means "not provided"
- no fuzzy match
- unknown `dept_name` returns: department not found
- duplicate `dept_name` returns: department name is ambiguous, use `dept_id`

This keeps the first version deterministic and avoids accidental matches such as `学生` matching `学生会`.

## Testing Strategy

Use TDD.

### Resolver Tests

Add focused unit tests for:

- resolve by `dept_name`
- `dept_name` takes precedence over `dept_id`
- fallback to `dept_id`
- no filter when both are empty
- unknown name
- duplicate name

### Tool Regression Tests

Add tests for all 4 affected tools to verify:

- `dept_name` is resolved into the expected `DeptID`
- invalid `dept_name` does not call downstream attendance or analytics ports
- legacy `dept_id` behavior remains intact

## Risks

- Existing tenants may contain duplicate leaf department names.
  In that case, the new behavior should fail closed and ask for `dept_id`.
- Tool descriptions must clearly prefer `dept_name`, or the LLM may continue using unfiltered calls.

## Verification Plan

At minimum, run:

- `go test ./internal/agent/tools -run Dept -v`
- `go test ./internal/agent/tools -v`
- `go test ./internal/agent/...`

If package naming or test names differ, use the equivalent focused commands that cover the new resolver and the 4 affected tools.
