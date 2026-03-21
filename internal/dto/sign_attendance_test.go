package dto

import "testing"

func TestSignForUserRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     SignForUserRequest
		wantErr bool
	}{
		{
			name: "record id and target users pass",
			req: SignForUserRequest{
				RecordID:      1,
				TargetUserIDs: []uint{2, 3},
			},
			wantErr: false,
		},
		{
			name: "date section and target users pass without record id",
			req: SignForUserRequest{
				Date:          "2026-03-21",
				Section:       2,
				TargetUserIDs: []uint{2, 3},
			},
			wantErr: false,
		},
		{
			name: "missing record id and incomplete date or section fail",
			req: SignForUserRequest{
				Date:          "2026-03-21",
				TargetUserIDs: []uint{2, 3},
			},
			wantErr: true,
		},
		{
			name: "empty target users fail",
			req: SignForUserRequest{
				RecordID: 1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
