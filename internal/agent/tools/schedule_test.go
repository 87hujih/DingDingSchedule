package tools

import (
	"testing"
	"time"
)

func TestWeekdayNumberForTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Weekday
		want int
	}{
		{
			name: "monday keeps one based value",
			in:   time.Monday,
			want: 1,
		},
		{
			name: "sunday maps to seven",
			in:   time.Sunday,
			want: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := weekdayNumberForTool(tt.in); got != tt.want {
				t.Fatalf("weekdayNumberForTool(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
