package tui

import (
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// The project-storage stint sheet in every state its bar changes shape in.
//
// THE SHEET'S BAR IS THE WEB'S ENFORCEMENT GATE, so its states are the stint's
// lifecycle: an ACTIVE stint offers neither the notice nor purgatory, an EXPIRED
// one offers the notice, a WARNED one offers both, and a warned one with OMS
// reporting email down offers purgatory and withholds the notice. A write in
// flight takes every write key off. The four confirm frames each have a bar of
// their own, at rest and with their write out — drawn in PLACE of the sheet, so
// the keys are on the pane however short it is.

// proseBarStint is a stint sheet past its load, in the given lifecycle status.
func proseBarStint(deps Deps, status string, mutate func(*omsapi.ProjectStorageStint)) *ProjectStorageDetailScreen {
	now := time.Now()
	started := now.AddDate(0, 0, -40)
	expires := started.AddDate(0, 0, 30)
	st := &omsapi.ProjectStorageStint{
		StintID: "PS-JWBRFM4F", Username: "alice", DisplayName: "Alice Smith",
		Email: "alice@example.org", SlotCode: "1A1", LocationDisplay: "1A1",
		ProjectTitle: "Powder-coating rig for the metal shop", Status: status,
		StartedAt: started, ExpiresAt: &expires, ExpiryWeek: 36, ExpiryDayOfYear: 247,
		QRCodeURL: "http://oms.example/media/project_storage/qrcodes/project_storage_qr_PS-JWBRFM4F.png",
	}
	if status == "purgatory_warned" {
		sent := now.AddDate(0, 0, -2)
		due := sent.AddDate(0, 0, 7)
		st.NoticeSentAt, st.PurgatoryAt = &sent, &due
	}
	if mutate != nil {
		mutate(st)
	}
	s := NewProjectStorageDetailScreen(deps, st.StintID)
	next, _ := s.Update(projectStorageDetailLoadedMsg{stint: st})
	return next.(*ProjectStorageDetailScreen)
}

// proseBarEmailDown is a Deps whose service health positively reports email
// degraded.
func proseBarEmailDown() Deps {
	h := NewServiceHealth()
	h.Set(&omsapi.ResilienceStatus{Degraded: true, Services: []omsapi.ServiceStatus{
		{Key: omsapi.ServiceKeyEmail, Label: "Email", State: omsapi.ServiceStateOpen},
	}})
	return Deps{Health: h}
}

func proseBarProjectStorageFixtures() []proseBarFixture {
	const confirmImmobile = "a confirm frame is drawn in place of the sheet and has nothing to scroll"
	const boxImmobile = "the frame's box has the focus, so the movement keys go into it"
	return []proseBarFixture{
		{
			name: "project storage detail/expired", recv: "ProjectStorageDetailScreen",
			build: func() proseBarScreen { return proseBarStint(Deps{}, "expired", nil) },
		},
		{
			name: "project storage detail/warned", recv: "ProjectStorageDetailScreen",
			build: func() proseBarScreen { return proseBarStint(Deps{}, "purgatory_warned", nil) },
		},
		// Email down: the notice comes off the bar and `n` declines on the status
		// row, which changes nothing on the pane — the web disables the button.
		{
			name: "project storage detail/warned, email down", recv: "ProjectStorageDetailScreen",
			build: func() proseBarScreen { return proseBarStint(proseBarEmailDown(), "purgatory_warned", nil) },
		},
		// A write out: every write key comes off, and the note says what is out.
		{
			name: "project storage detail/generating QR", recv: "ProjectStorageDetailScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarStint(Deps{}, "purgatory_warned", nil), "q") },
		},
		{
			name: "project storage detail/notice confirm", recv: "ProjectStorageDetailScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarStint(Deps{}, "expired", nil), "n") },
			immobile: confirmImmobile,
		},
		{
			name: "project storage detail/notice sending", recv: "ProjectStorageDetailScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarStint(Deps{}, "expired", nil), "n", "y") },
			immobile: confirmImmobile,
		},
		{
			name: "project storage detail/purgatory confirm", recv: "ProjectStorageDetailScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarStint(Deps{}, "purgatory_warned", nil), "P") },
			immobile: boxImmobile,
		},
		{
			name: "project storage detail/purgatory moving", recv: "ProjectStorageDetailScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarStint(Deps{}, "purgatory_warned", nil), "P", "enter")
			},
			immobile: confirmImmobile,
		},
		{
			name: "project storage detail/remove confirm", recv: "ProjectStorageDetailScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarStint(Deps{}, "active", nil), "x") },
			immobile: boxImmobile,
		},
		{
			name: "project storage detail/reprint confirm", recv: "ProjectStorageDetailScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarStint(Deps{}, "active", nil), "p") },
			immobile: confirmImmobile,
		},
	}
}
