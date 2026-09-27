package domain

import "testing"

func TestSessionMetaNormalize(t *testing.T) {
	t.Run("defaults empty AAL and nil AMR", func(t *testing.T) {
		got := SessionMeta{}.Normalize()
		if got.AAL != "aal1" {
			t.Errorf("AAL = %q, want aal1", got.AAL)
		}
		if got.AMR == nil {
			t.Error("AMR must be non-nil (so it marshals as [] not null)")
		}
		if len(got.AMR) != 0 {
			t.Errorf("AMR = %v, want empty", got.AMR)
		}
	})

	t.Run("preserves set AAL and AMR", func(t *testing.T) {
		amr := []AMREntry{{Method: "totp", Timestamp: 1}}
		got := SessionMeta{AAL: "aal2", AMR: amr}.Normalize()
		if got.AAL != "aal2" {
			t.Errorf("AAL = %q, want aal2", got.AAL)
		}
		if len(got.AMR) != 1 || got.AMR[0].Method != "totp" {
			t.Errorf("AMR = %v, want unchanged", got.AMR)
		}
	})

	t.Run("preserves empty non-nil AMR", func(t *testing.T) {
		empty := []AMREntry{}
		got := SessionMeta{AAL: "aal1", AMR: empty}.Normalize()
		if got.AMR == nil {
			t.Error("AMR must stay non-nil")
		}
	})

	t.Run("does not mutate the receiver", func(t *testing.T) {
		m := SessionMeta{}
		_ = m.Normalize()
		if m.AAL != "" || m.AMR != nil {
			t.Errorf("receiver mutated: %+v", m)
		}
	})
}
