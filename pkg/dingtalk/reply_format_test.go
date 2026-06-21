package dingtalk

import "testing"

func TestRenderPlainTextReplyDowngradesCommonMarkdown(t *testing.T) {
	t.Parallel()

	input := "### 考勤统计\n\n- **出勤次数**：12\n- **迟到次数**：2\n- `状态`：正常\n- [查看详情](https://example.com/detail)\n"
	want := "考勤统计\n\n- 出勤次数：12\n- 迟到次数：2\n- 状态：正常\n- 查看详情：https://example.com/detail"

	if got := renderPlainTextReply(input); got != want {
		t.Fatalf("renderPlainTextReply() = %q, want %q", got, want)
	}
}

func TestRenderPlainTextReplyCollapsesExcessiveBlankLines(t *testing.T) {
	t.Parallel()

	input := "# 标题\n\n\n内容一\n\n\n\n内容二\n"
	want := "标题\n\n内容一\n\n内容二"

	if got := renderPlainTextReply(input); got != want {
		t.Fatalf("renderPlainTextReply() = %q, want %q", got, want)
	}
}

func TestRenderPlainTextReplyKeepsPlainTextStable(t *testing.T) {
	t.Parallel()

	input := "当前群已订阅考勤推送。"
	want := "当前群已订阅考勤推送。"

	if got := renderPlainTextReply(input); got != want {
		t.Fatalf("renderPlainTextReply() = %q, want %q", got, want)
	}
}
