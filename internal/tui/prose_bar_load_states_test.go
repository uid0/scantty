package tui

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_load_states_test.go — the bar-honesty rule in the two states every
// earlier conversion left out: a load IN FLIGHT and a load that FAILED.
//
// THE DEFECT, measured rather than assumed. Every converted screen with a load
// answered nil from proseBar while `loading || loadErr != ""` and drew a literal
// naming a retry and a way back. Its key switch did not stop answering. Driving
// each screen's key switch in those states — this sweep, against that shape —
// found `w`/`a` still reloading the forecasts from a failed load, a
// create key (`c`, `n`) opening its form on every list that has one, `X`
// marking every notification read under "Loading notifications…", and — after a
// REFRESH failed, which keeps the previous rows on most lists — `E`, `enter` and
// the row actions (`p` set-primary on an item's suppliers, `d`/`o` on a breaker's
// circuits) switching screens or writing against a row the frame no longer
// draws. The frame named two keys where the screen worked up to six.
//
// WHAT IS SWEPT, AND WHY FOUR STATES PER SCREEN. A first load and a refresh are
// different states of the same flag: the first has no rows at all, and a
// refresh started from a loaded list keeps whatever rows it had — which is where
// every stale-row key lives. So each screen is driven LOADING, LOAD FAILED,
// RELOADING and RELOAD FAILED, the refresh ones reached by pressing the reload key
// on a loaded fixture from proseBarFixtures rather than by setting a flag, and
// the failures by delivering the message the screen's own load command returned
// against a backend that refuses everything (proseLoadBackend). The set of screens
// is DERIVED — every proseBar receiver whose type declares a load flag — and
// TestProseBar_EveryScreenWithALoadIsSweptInIt fails in both directions.
//
// THE INSTRUMENT IS STRONGER THAN THE PANE, and that is the whole reason this is
// a sweep of its own rather than four more rows in the biconditional. A loading
// or failed frame draws one line and a bar; almost nothing a key does there
// changes that line. What the keys above do is ISSUE A COMMAND — a switch to a
// form, a reload, a write — so a pane-only reverse half (the one
// TestProseBar_TheFooterNamesExactlyTheKeysThatWork uses, for the reason it
// gives about toasts) passes every one of them. Here a key ACTS if it changes
// the clipped pane OR its command produces a message that is not a status toast
// (proseLoadKeyEffect runs it, bounded). A command producing ONLY a StatusMsg is
// a decline that says why, exactly as on the loaded frames, and is not an act —
// though it is not a DEAD key either, so a bar naming a key whose answer is a
// toast passes the forward half, the same asymmetry the loaded biconditional
// keeps (a staff-only `n grant` pressed without staff answers why, and is still
// the key the bar is right to name).
//
// WHAT IT DOES NOT CALL AN ACT, said so the table is not over-read. A key whose
// only effect would be a field no frame draws — a cursor walking stale rows,
// `x` arming a delete confirm under the error, or `/` focusing a search box
// nobody can see — is ignored. The runtime sweep below reveals the underlying
// frame after each unnamed key and proves that no hidden state was entered.

// proseLoadState is which half of a load a fixture is in.
type proseLoadState string

const (
	proseLoadInFlight   proseLoadState = "loading"
	proseLoadFailed     proseLoadState = "load failed"
	proseReloadInFlight proseLoadState = "reloading"
	proseReloadFailed   proseLoadState = "reload failed"
	// proseLoadRefused is a load the server REFUSED (403) on a screen that draws
	// a refusal differently from a failure — the analytics pulse's staff-only
	// notice.
	proseLoadRefused proseLoadState = "load refused"
)

// proseLoadScreen is one screen with a load, and what driving it takes.
type proseLoadScreen struct {
	name string
	recv string
	// fresh builds the screen against deps, in the state its constructor leaves
	// it: loading, with nothing loaded yet.
	fresh func(Deps) proseBarScreen
	// loaded names the fixture in proseBarFixtures a REFRESH starts from.
	loaded string
	// reload is the key that refreshes a loaded screen. noReload says why a
	// screen has none, in which case the two refresh states are not built.
	reload   string
	noReload string
	// refused builds the 403 state as well, for a screen that draws one.
	refused bool
	// failureIsAForm says why a FAILED load on this screen is drawn by the
	// screen's own loaded frame rather than by the load frame, which takes it
	// out of the give-order claim the load-frame bar sweep makes — and only
	// that one; every other sweep still presses it.
	failureIsAForm string
	// loadCmd, where set, is the command carrying the load of a screen fresh
	// builds with its load already out — for a screen whose load is not started
	// by Init. The search palette's is a typed query's search, sent when the
	// debounce fires; Init only blinks the caret, so running it would deliver no
	// failure and the failed states would silently be something else.
	loadCmd func(proseBarScreen) tea.Cmd
	// typing is proseBarFixture.typing for every load state of this screen: a
	// focused box drawn on the load frame itself.
	typing string
	// rowsStay says why a REFRESH on this screen keeps its rows DRAWN, where
	// every other screen's load frame draws none — so its refresh-in-flight
	// state is swept as a list that moves rather than recorded immobile.
	rowsStay string
	// ownFloor says why this screen's load frames need more of the pane than the
	// lead row, the blank and the bar before they can keep the bar whole — and so
	// why ALoadFrameKeepsItsBar asks the screen's own give-order (frameFits)
	// where that boundary is, rather than the shared frames' arithmetic. A screen
	// recording it must answer frameFits, and one answering it must record why.
	ownFloor string
	// restamp adapts cached load replies when a screen identifies response
	// batches and the fixture replays a first-load reply onto a reload.
	restamp func(proseBarScreen, []tea.Msg) []tea.Msg
}

// proseLoadReportFloor is the report table's reason, said once for both reports.
const proseLoadReportFloor = "the report table draws its tab bar and the blank under " +
	"it above every load frame, because ←/→ [/] switch report from there, and it " +
	"floors a failure at its first line AND the row saying the rest was cut " +
	"(reportErrMinRows). Its frameFits is that give-order's own answer, and " +
	"TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas holds it"

// proseLoadFloored is a screen whose give-order answers where its frame's floor
// is — see proseLoadScreen.ownFloor.
type proseLoadFloored interface{ frameFits() bool }

// proseLoadPerTab is a screen whose load is not a pair of fields on the screen
// itself but a fact about what it is standing on — the report table keeps one
// per TAB. loadState is what its own bar and key gate read; revealLoad is what
// the hidden-state sweep calls in place of clearing the fields.
type proseLoadPerTab interface {
	loadState() (loading bool, loadErr string)
	revealLoad()
}

func proseLoadScreens() []proseLoadScreen {
	return append(proseBarFixtureLoadScreens(), []proseLoadScreen{
		{name: "analytics pulse", recv: "AnalyticsPulseScreen", loaded: "analytics pulse", reload: "r", refused: true,
			fresh: func(d Deps) proseBarScreen { return NewAnalyticsPulseScreen(d) }},
		{name: "asset detail", recv: "AssetDetailScreen", loaded: "asset detail/components", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewAssetDetailScreen(d, "a-1") }},
		{name: "asset problems", recv: "AssetProblemsScreen", loaded: "asset problems", reload: "r",
			fresh: func(d Deps) proseBarScreen {
				return NewAssetProblemsScreen(d, "a-1", "Haas VF-2SS vertical machining centre")
			}},
		{name: "authorizations", recv: "AuthorizationsScreen", loaded: "authorizations", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewAuthorizationsScreen(d) }},
		{name: "badge enrollment", recv: "BadgeEnrollmentScreen", loaded: "badge enrollment", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewBadgeEnrollmentScreen(d) }},
		{name: "breaker circuits", recv: "BreakerCircuitsScreen", loaded: "breaker circuits", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewBreakerCircuitsScreen(d, 7, 1, "Bay 7 receptacles") }},
		{name: "category list", recv: "CategoryListScreen", loaded: "category list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewCategoryListScreen(d) }},
		{name: "checklist run", recv: "ChecklistRunScreen", loaded: "checklist run", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewChecklistRunScreen(d, "cmpl-1") }},
		{name: "checklists", recv: "ChecklistsScreen", loaded: "checklists", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewChecklistsScreen(d) }},
		{name: "circuit disconnects", recv: "CircuitDisconnectsScreen", loaded: "circuit disconnects", reload: "r",
			fresh: func(d Deps) proseBarScreen {
				return NewCircuitDisconnectsScreen(d, 3, 1, "Bay 7 receptacles, north run")
			}},
		{name: "circuit outlets", recv: "CircuitOutletsScreen", loaded: "circuit outlets", reload: "r",
			fresh: func(d Deps) proseBarScreen {
				return NewCircuitOutletsScreen(d, 3, 1, "Bay 7 receptacles, north run")
			}},
		{name: "demand forecast", recv: "DemandForecastScreen", loaded: "demand forecast", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewDemandForecastScreen(d) }},
		{name: "device type list", recv: "DeviceTypeListScreen", loaded: "device type list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewDeviceTypeListScreen(d) }},
		{name: "donations", recv: "DonationsScreen", loaded: "donations", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewDonationsScreen(d) }},
		{name: "electrical panel detail", recv: "ElectricalPanelDetailScreen", loaded: "electrical panel detail", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewElectricalPanelDetailScreen(d, 1) }},
		{name: "electrical panels", recv: "ElectricalPanelsScreen", loaded: "electrical panels", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewElectricalPanelsScreen(d) }},
		{name: "e-paper panels", recv: "EPaperPanelsScreen", loaded: "e-paper panels", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewEPaperPanelsScreen(d) }},
		{name: "certificates", recv: "ForgeKeyCertificatesScreen", loaded: "certificates", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewForgeKeyCertificatesScreen(d) }},
		{name: "firmware", recv: "FirmwareScreen", loaded: "firmware", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewFirmwareScreen(d) }},
		{name: "forgekey device form", recv: "ForgeKeyDeviceFormScreen",
			failureIsAForm: "a failed location load drops into the edit phase with the failure on " +
				"one row above the form's own bar — a fixed seven-row field form with no give-order, " +
				"swept where it fits like the other field forms",
			noReload: "the form loads its location list once, on the way in, and no key reloads " +
				"it — a failed load drops into the edit phase with the failure drawn above the " +
				"bar, which is a loaded-state fixture of its own",
			fresh: func(d Deps) proseBarScreen {
				return NewForgeKeyDeviceFormScreen(d, &forgekeyapi.Device{ID: 12, Name: "Wood shop south door reader"})
			}},
		{name: "item detail", recv: "InventoryDetailScreen", loaded: "item detail/serialized, open-closed, retired", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewInventoryDetailScreen(d, "itm-1") }},
		{name: "item history", recv: "ItemHistoryScreen", loaded: "item history/stock", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewItemHistoryScreen(d, proseBarHistoryItem()) },
			restamp: func(s proseBarScreen, msgs []tea.Msg) []tea.Msg {
				loadID := s.(*ItemHistoryScreen).loadID
				out := append([]tea.Msg(nil), msgs...)
				for i, msg := range out {
					switch m := msg.(type) {
					case itemHistoryStockMsg:
						m.loadID = loadID
						out[i] = m
					case itemHistoryUsageMsg:
						m.loadID = loadID
						out[i] = m
					}
				}
				return out
			}},
		{name: "item suppliers", recv: "ItemSuppliersScreen", loaded: "item suppliers", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewItemSuppliersScreen(d, "itm-1", "Hex bolt M8x40") }},
		{name: "location check-ins", recv: "LocationCheckinsScreen", loaded: "location check-ins", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewLocationCheckinsScreen(d) }},
		{name: "location list", recv: "LocationListScreen", loaded: "location list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewLocationListScreen(d) }},
		{name: "location problems", recv: "LocationProblemsScreen", loaded: "location problems", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewLocationProblemsScreen(d, 7, "Wood shop, south wall") }},
		{name: "lockouts", recv: "LockoutsScreen", loaded: "lockouts", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewLockoutsScreen(d) }},
		{name: "maker boxes", recv: "MakerBoxesScreen", loaded: "maker boxes", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewMakerBoxesScreen(d) }},
		{name: "maintenance item detail", recv: "MaintenanceItemDetailScreen", loaded: "maintenance item detail", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewMaintenanceItemDetailScreen(d, "pm-1") }},
		{name: "maintenance items", recv: "MaintenanceItemsScreen", loaded: "maintenance items", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewMaintenanceItemsScreen(d) }},
		{name: "notifications", recv: "NotificationsScreen", loaded: "notifications", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewNotificationsScreen(d) }},
		{name: "operational modes", recv: "OperationalModesScreen", loaded: "operational modes", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewOperationalModesScreen(d) }},
		{name: "panel breakers", recv: "PanelBreakersScreen", loaded: "panel breakers", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewPanelBreakersScreen(d, 1, "Main distribution panel MDP-1") }},
		{name: "pm board", recv: "PMBoardScreen", loaded: "pm board", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewPMBoardScreen(d) }},
		{name: "project storage detail", recv: "ProjectStorageDetailScreen", loaded: "project storage detail", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewProjectStorageDetailScreen(d, "PS-AB23CDFG") }},
		// THE REPORT TABLE ON TWO REPORTS, because one type rides every tabbed
		// report and a load is a property of the TAB it is standing on: the
		// purchasing report opens on a supplier-spend table and the asset report
		// on a status count, each loaded, refreshed and failed on the tab it opens
		// on. Its load frames spend two rows the shared frames do not — the tab
		// bar and the blank under it, kept because ←/→ [/] act on that bar — so
		// its floor is its own (ownFloor).
		{name: "purchasing report", recv: "ReportTableScreen", loaded: "purchasing report", reload: "r",
			ownFloor: proseLoadReportFloor,
			fresh:    func(d Deps) proseBarScreen { return NewPurchasingReportScreen(d) }},
		{name: "asset report", recv: "ReportTableScreen", loaded: "asset report", reload: "r",
			ownFloor: proseLoadReportFloor,
			fresh:    func(d Deps) proseBarScreen { return NewAssetReportScreen(d) }},
		{name: "reorder queue", recv: "ReorderQueueScreen", loaded: "reorder queue/pending", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewReorderQueueScreen(d) }},
		// THE PALETTE HAS NO REFRESH KEY, because editing the query IS the refresh:
		// every edit schedules a search over the results still drawn. So its reload
		// is BACKSPACE on a loaded query one character longer than the one its load
		// starts from, which lands the refresh on exactly the query the failing
		// backend was asked — a failure for any other query is dropped as stale,
		// and the fixture would be left in a state it is not named for.
		{name: "search palette", recv: "SearchPalette", loaded: "search palette/results", reload: "backspace",
			typing: proseBarFormBox,
			rowsStay: "a refined query is searched OVER the results it is narrowing, which stay " +
				"on the pane with the cursor on them, so the arrows visibly move it while the " +
				"search is out",
			fresh: func(d Deps) proseBarScreen { return proseBarPaletteTyped(d, proseBarPaletteLoadQuery) },
			loadCmd: func(s proseBarScreen) tea.Cmd {
				_, cmd := s.Update(searchTickMsg{query: proseBarPaletteLoadQuery})
				return cmd
			}},
		{name: "serialized components", recv: "SerializedComponentsScreen", loaded: "serialized components/list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewItemInstancesScreen(d, "item-1", "Safety relay", nil) }},
		{name: "serialized forecast", recv: "SerializedForecastScreen", loaded: "serialized forecast", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewSerializedForecastScreen(d) }},
		{name: "sig detail", recv: "SIGDetailScreen", loaded: "sig detail", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewSIGDetailScreen(d, "3") }},
		{name: "sig list", recv: "SIGListScreen", loaded: "sig list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewSIGListScreen(d) }},
		{name: "sig members", recv: "SIGMembersScreen", loaded: "sig members", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewSIGMembersScreen(d, 3, "Metal Fabrication SIG") }},
		{name: "storage slots", recv: "StorageSlotsScreen", loaded: "storage slots", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewStorageSlotsScreen(d) }},
		{name: "storage overview", recv: "StorageOverviewScreen", loaded: "storage overview", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewStorageOverviewScreen(d) }},
		{name: "storage slot detail", recv: "StorageSlotDetailScreen", loaded: "storage slot detail/free", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewStorageSlotDetailScreen(d, "1A1") }},
		{name: "supplier detail", recv: "SupplierDetailScreen", loaded: "supplier detail", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewSupplierDetailScreen(d, "4") }},
		{name: "supplier list", recv: "SupplierListScreen", loaded: "supplier list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewSupplierListScreen(d) }},
		{name: "thermostat list", recv: "ThermostatListScreen", loaded: "thermostat list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewThermostatListScreen(d) }},
		{name: "usage sessions", recv: "UsageScreen", loaded: "usage sessions", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewUsageScreen(d) }},
		{name: "vendors", recv: "VendorsScreen", loaded: "vendors", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewVendorsScreen(d) }},
		{name: "work order detail", recv: "WorkOrderDetailScreen", loaded: "work order detail", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewWorkOrderDetailScreen(d, "wo1") }},
		{name: "work order attachments", recv: "WorkOrderAttachmentsScreen", loaded: "work order attachments", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewWorkOrderAttachmentsScreen(d, "42") }},
		{name: "webhook list", recv: "WebhookListScreen", loaded: "webhook list", reload: "r",
			fresh: func(d Deps) proseBarScreen { return NewWebhookListScreen(d) }},
		{name: "asset parts", recv: "AssetPartsScreen", loaded: "asset parts", reload: "r",
			fresh: func(d Deps) proseBarScreen {
				return NewAssetPartsScreen(d, "a-1", "Haas VF-2SS vertical machining centre")
			}},
	}...)
}

// proseLoadGatewayPage is what the failing backend answers with: nginx's 502
// page, the body omsapi.parseError hands over whole because it carries no JSON
// envelope. It is MULTI-LINE on purpose — a failure frame that writes an OMS
// body out unbounded pushes its own bar off the pane, and a one-line fixture
// error could never show it.
const proseLoadGatewayPage = "<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n" +
	"<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

var proseLoadBackends = struct {
	sync.Mutex
	byStatus map[int]*httptest.Server
}{byStatus: map[int]*httptest.Server{}}

// proseLoadBackend is a backend refusing every request with this status, shared
// for the life of the test binary: fixtures are rebuilt thousands of times and a
// server per build would be the cost of the sweep.
func proseLoadBackend(status int) *httptest.Server {
	proseLoadBackends.Lock()
	defer proseLoadBackends.Unlock()
	if srv, ok := proseLoadBackends.byStatus[status]; ok {
		return srv
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusForbidden {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, `{"detail":"You do not have permission to perform this action."}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		fmt.Fprint(w, proseLoadGatewayPage)
	}))
	proseLoadBackends.byStatus[status] = srv
	return srv
}

// proseLoadDeps is a Deps whose OMS and ForgeKey clients both reach a backend
// refusing everything with this status.
func proseLoadDeps(status int) Deps {
	srv := proseLoadBackend(status)
	fk, err := forgekeyapi.New(forgekeyapi.Options{BaseURL: srv.URL})
	if err != nil {
		panic(err)
	}
	return Deps{OMS: omsapi.New(srv.URL), ForgeKey: fk, Ctx: context.Background()}
}

var proseLoadReplies = struct {
	sync.Mutex
	byKey map[string][]tea.Msg
}{byKey: map[string][]tea.Msg{}}

// proseLoadFailure is what a screen's own load command answers against a
// backend refusing everything with this status — run ONCE per screen and
// replayed, because Update is a function of the messages it is handed and the
// sweep rebuilds every fixture per key.
func proseLoadFailure(ls proseLoadScreen, status int) []tea.Msg {
	key := fmt.Sprintf("%s/%d", ls.name, status)
	proseLoadReplies.Lock()
	defer proseLoadReplies.Unlock()
	if msgs, ok := proseLoadReplies.byKey[key]; ok {
		return msgs
	}
	s := ls.fresh(proseLoadDeps(status))
	cmd := s.Init()
	if ls.loadCmd != nil {
		cmd = ls.loadCmd(s)
	}
	msgs, _ := proseLoadRun(cmd, 0)
	proseLoadReplies.byKey[key] = msgs
	return msgs
}

// proseLoadRun runs a command the way the program would, BOUNDED, and returns
// the messages it produced — a batch flattened, the caret blink dropped (it
// carries nothing, and feeding it back is what starts a tick; see driveIsBlink).
//
// `reached` reports that the command touched the backend where no backend was
// wired: a fixture built on a bare Deps{} panics on the nil client, and a key
// that got that far has ACTED — it sent a request.
func proseLoadRun(cmd tea.Cmd, depth int) (msgs []tea.Msg, reached bool) {
	if cmd == nil || depth > 8 {
		return nil, false
	}
	type result struct {
		msg      tea.Msg
		panicked bool
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{panicked: true}
			}
		}()
		done <- result{msg: cmd()}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(2 * time.Second):
		// A command still running after two seconds against a local backend is
		// a timer or a hang, and either is the key having started something.
		return nil, true
	}
	if r.panicked {
		return nil, true
	}
	if r.msg == nil || driveIsBlink(r.msg) {
		return nil, false
	}
	if batch, ok := r.msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m, hit := proseLoadRun(c, depth+1)
			msgs = append(msgs, m...)
			reached = reached || hit
		}
		return msgs, reached
	}
	return []tea.Msg{r.msg}, false
}

func proseLoadDeliver(s proseBarScreen, msgs []tea.Msg) proseBarScreen {
	for _, m := range msgs {
		next, _ := s.Update(m)
		s = next.(proseBarScreen)
	}
	return s
}

func proseLoadRepliesFor(ls proseLoadScreen, s proseBarScreen, status int) []tea.Msg {
	msgs := proseLoadFailure(ls, status)
	if ls.restamp != nil {
		msgs = ls.restamp(s, msgs)
	}
	return msgs
}

// proseBarLoadStateFixtures builds every screen in proseLoadScreens in each of
// its load states, the refresh ones from the loaded fixture named in `drawn`.
func proseBarLoadStateFixtures(drawn []proseBarFixture) []proseBarFixture {
	byName := map[string]proseBarFixture{}
	for _, f := range drawn {
		byName[f.name] = f
	}
	const immobile = "a load in flight or failed draws no rows, so no movement key has " +
		"anything on the pane to move — a cursor walking the rows a refresh kept is a " +
		"field nothing draws, which is a gating candidate and not a movement"
	var out []proseBarFixture
	for _, ls := range proseLoadScreens() {
		ls := ls
		add := func(state proseLoadState, build func() proseBarScreen) {
			still := immobile
			if state == proseReloadInFlight && ls.rowsStay != "" {
				still = ""
			}
			out = append(out, proseBarFixture{
				name: ls.name + "/" + string(state), recv: ls.recv, load: state,
				build: build, immobile: still, typing: ls.typing,
			})
		}
		add(proseLoadInFlight, func() proseBarScreen { return ls.fresh(proseLoadDeps(http.StatusBadGateway)) })
		add(proseLoadFailed, func() proseBarScreen {
			s := ls.fresh(proseLoadDeps(http.StatusBadGateway))
			return proseLoadDeliver(s, proseLoadRepliesFor(ls, s, http.StatusBadGateway))
		})
		if ls.refused {
			add(proseLoadRefused, func() proseBarScreen {
				s := ls.fresh(proseLoadDeps(http.StatusForbidden))
				return proseLoadDeliver(s, proseLoadRepliesFor(ls, s, http.StatusForbidden))
			})
		}
		if ls.reload == "" {
			continue
		}
		loaded, ok := byName[ls.loaded]
		if !ok {
			// Reported by TestProseBar_EveryScreenWithALoadIsSweptInIt; building
			// nothing here keeps the roster itself from panicking.
			continue
		}
		reloading := func() proseBarScreen {
			s := proseBarSize(loaded.build(), 80, 24)
			next, _ := s.Update(listRuneKey(ls.reload))
			return next.(proseBarScreen)
		}
		add(proseReloadInFlight, reloading)
		add(proseReloadFailed, func() proseBarScreen {
			s := reloading()
			return proseLoadDeliver(s, proseLoadRepliesFor(ls, s, http.StatusBadGateway))
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Coverage
// ---------------------------------------------------------------------------

// proseBarLoadReceivers is every proseBar receiver whose type declares a load
// flag — a `loading` or `loadErr` field — read out of the package source.
//
// DERIVED FROM THE TYPE AND NOT FROM THE proseBar METHOD, because the method is
// the thing that was wrong: a screen whose proseBar never mentions its load
// flags (the reorder queue's delegates to barFor) still HAS a load, and a
// derivation reading the method would have excused exactly the screens that
// forgot to answer for it.
func proseBarLoadReceivers(t *testing.T) map[string]bool {
	t.Helper()
	converted := proseBarReceivers(t)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	// Two passes, because a load flag need not sit on the screen itself: the
	// report table keeps one per TAB, in a []reportTabState, and a derivation
	// reading only the screen's own fields excused it from this sweep while its
	// movement keys walked the rows a refresh kept under "Loading …". So the
	// first pass finds every struct declaring a flag, and a screen has a load
	// if it declares one or holds such a struct — directly, by pointer or as a
	// slice of them.
	structs := map[string]*ast.StructType{}
	for _, file := range pkgs["tui"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			if spec, ok := n.(*ast.TypeSpec); ok {
				if st, ok := spec.Type.(*ast.StructType); ok {
					structs[spec.Name.Name] = st
				}
			}
			return true
		})
	}
	flagged := func(st *ast.StructType) bool {
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				if name.Name == "loading" || name.Name == "loadErr" {
					return true
				}
			}
		}
		return false
	}
	out := map[string]bool{}
	for name, st := range structs {
		if !converted[name] {
			continue
		}
		if flagged(st) {
			out[name] = true
			continue
		}
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 {
				continue
			}
			typ := field.Type
			switch t := typ.(type) {
			case *ast.StarExpr:
				typ = t.X
			case *ast.ArrayType:
				typ = t.Elt
			}
			if id, ok := typ.(*ast.Ident); ok && structs[id.Name] != nil && flagged(structs[id.Name]) {
				out[name] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no converted screen declares a load flag, so the derivation is broken, not the app")
	}
	return out
}

// TestProseBar_EveryScreenWithALoadIsSweptInIt: the load-state roster and the
// screens that have a load agree, in both directions, and every entry builds
// the states it claims.
func TestProseBar_EveryScreenWithALoadIsSweptInIt(t *testing.T) {
	want := proseBarLoadReceivers(t)
	drawn := map[string]bool{}
	for _, f := range proseBarFixtures() {
		if f.load == "" {
			drawn[f.name] = true
		}
	}
	got := map[string]bool{}
	for _, ls := range proseLoadScreens() {
		got[ls.recv] = true
		if !want[ls.recv] {
			t.Errorf("proseLoadScreens drives %s, which is not a converted screen with a load "+
				"flag — a stale entry spends the sweep on a screen that no longer has the state", ls.recv)
		}
		if ls.reload != "" && !drawn[ls.loaded] {
			t.Errorf("%s refreshes from the fixture %q, which proseBarFixtures does not build, so "+
				"its two refresh states are never driven", ls.name, ls.loaded)
		}
		if _, floored := ls.fresh(Deps{}).(proseLoadFloored); floored != (ls.ownFloor != "") {
			t.Errorf("%s: a screen answering frameFits must record why its load frames floor "+
				"there (ownFloor), and one recording it must answer frameFits — the sweep "+
				"that reads it would otherwise be scoped by a boundary nobody stated", ls.name)
		}
		if (ls.reload == "") == (ls.noReload == "") {
			t.Errorf("%s must say either which key reloads it or why none does — absent and "+
				"empty are different states", ls.name)
		}
	}
	for recv := range want {
		if !got[recv] {
			t.Errorf("%s declares proseBar and a load flag, but proseLoadScreens does not drive "+
				"it, so nothing presses a key at it while its load is out or has failed — the "+
				"state this sweep exists for", recv)
		}
	}
}

// TestProseBar_EveryLoadStateFixtureIsInItsState: a load-state fixture really is
// loading, or really has failed.
//
// THE VACUITY GUARD, for the reason TestProseBar_EveryConvertedScreenIsSwept
// gives on the other side. A failure that never landed — a message delivered to a
// screen that drops it as stale, a reload key that is not the reload — leaves a
// fixture in a state the sweep then reports on under the wrong name, and a
// "reload failed" that is really a loaded list passes by being honest about
// something else. Read off the screen's own flags, because asserting the frame
// would be asserting the wording under test.
func TestProseBar_EveryLoadStateFixtureIsInItsState(t *testing.T) {
	flag := func(s proseBarScreen, name string) (reflect.Value, bool) {
		v := reflect.ValueOf(s).Elem().FieldByName(name)
		return v, v.IsValid()
	}
	seen := 0
	for _, f := range proseBarFixtures() {
		if f.load == "" {
			continue
		}
		seen++
		s := proseBarSize(f.build(), 80, 24)
		if tabbed, ok := s.(proseLoadPerTab); ok {
			loading, loadErr := tabbed.loadState()
			switch f.load {
			case proseLoadInFlight, proseReloadInFlight:
				if !loading {
					t.Errorf("the %s fixture is not loading", f.name)
				}
			case proseLoadFailed, proseReloadFailed:
				if loadErr == "" {
					t.Errorf("the %s fixture has no load failure recorded — the failure never "+
						"landed, and the sweep would be reporting on another state", f.name)
				}
			default:
				t.Errorf("the %s fixture is in %q, which a per-tab load cannot report", f.name, f.load)
			}
			continue
		}
		switch f.load {
		case proseLoadInFlight, proseReloadInFlight:
			if v, ok := flag(s, "loading"); ok {
				if !v.Bool() {
					t.Errorf("the %s fixture is not loading", f.name)
				}
			} else if v, ok := flag(s, "phase"); !ok || v.Int() != 0 {
				t.Errorf("the %s fixture declares neither a `loading` flag nor a loading phase "+
					"at zero, so nothing says it is in flight", f.name)
			}
		case proseLoadFailed, proseReloadFailed:
			if v, ok := flag(s, "loadErr"); !ok || v.String() == "" {
				t.Errorf("the %s fixture has no load failure recorded — the failure never "+
					"landed, and the sweep would be reporting on another state", f.name)
			}
		case proseLoadRefused:
			if v, ok := flag(s, "forbidden"); !ok || !v.Bool() {
				t.Errorf("the %s fixture was not refused", f.name)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no load-state fixture was built, so this guard asserted nothing")
	}
}

// ---------------------------------------------------------------------------
// The rule
// ---------------------------------------------------------------------------

// TestProseBar_ALoadInFlightOrFailedNamesExactlyTheKeysThatWork is the
// biconditional in the load states, with the instrument the file header argues
// for: a key ACTS if it changes the clipped pane or its command produces a
// message other than a status toast.
//
// VERIFIED BY REVERTING: with every proseBar answering nil in these states — the
// shape this replaced — it reports `w` on both forecasts, the create key on every
// list that has one, `X` on the notifications, and the stale-row keys after a
// failed refresh, as acting unnamed.
func TestProseBar_ALoadInFlightOrFailedNamesExactlyTheKeysThatWork(t *testing.T) {
	const w, h = 80, 24
	offPane := 0
	for _, f := range proseBarFixtures() {
		if f.load == "" {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			bar := proseBarAt(f, w, h)
			for _, key := range proseBarKeySpace() {
				named := bar.names(key)
				if !named && proseBarBoxTakes(f, key) {
					// The box's key, not the bar's: a box drawn ON the load frame
					// takes what is typed, and the loaded biconditional already
					// requires every such character to visibly land in it.
					continue
				}
				changed, acted, toasted := proseLoadKeyEffect(f, w, h, key)
				switch {
				case named && !changed && !acted && !toasted:
					if proseBarLeaves(t, f, key) {
						continue
					}
					t.Errorf("%s names %q but the key does nothing there — it changes no pixel "+
						"of the pane, issues no command and does not leave the screen.\nbar: %s", f.name, key, bar.hint())
				case !named && (changed || acted):
					if f.declines[key] != "" {
						continue
					}
					t.Errorf("%s does not name %q, but pressing it %s.\nbar: %s", f.name, key,
						proseLoadActWords(changed, acted), bar.hint())
				case named && acted && !changed:
					offPane++
				}
			}
		})
	}
	if offPane == 0 {
		t.Error("no load-state fixture named a key whose act was OFF the pane — a command " +
			"with no visible change — so the half of the instrument that is stronger than " +
			"the pane was never exercised, and this sweep says no more than the biconditional")
	}
}

func TestProseBar_UnnamedLoadKeysDoNotEnterHiddenStates(t *testing.T) {
	const w, h = 80, 24
	reveal := func(s proseBarScreen) string {
		if tabbed, ok := s.(proseLoadPerTab); ok {
			tabbed.revealLoad()
			return clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
		}
		v := reflect.ValueOf(s).Elem()
		if f := v.FieldByName("loading"); f.IsValid() && f.CanSet() {
			f.SetBool(false)
		}
		if f := v.FieldByName("loadErr"); f.IsValid() && f.CanSet() {
			f.SetString("")
		}
		if f := v.FieldByName("forbidden"); f.IsValid() && f.CanSet() {
			f.SetBool(false)
		}
		return clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
	}
	for _, f := range proseBarFixtures() {
		if f.load == "" {
			continue
		}
		for _, key := range proseBarKeySpace() {
			if proseBarAt(f, w, h).names(key) || proseBarBoxTakes(f, key) {
				// Named, or typed into a box the load frame draws — a query edited
				// in plain sight is not a hidden state.
				continue
			}
			want := reveal(proseBarSize(f.build(), w, h))
			s := proseBarSize(f.build(), w, h)
			next, _ := s.Update(listRuneKey(key))
			got := reveal(next.(proseBarScreen))
			if got != want {
				t.Errorf("%s: unnamed %q enters a state hidden by the load frame", f.name, key)
			}
		}
	}
}

func TestCategoryList_LoadStatesIgnoreDeleteConfirmation(t *testing.T) {
	var loaded proseBarFixture
	for _, f := range proseBarFixtures() {
		if f.name == "category list" {
			loaded = f
			break
		}
	}
	for _, failed := range []bool{false, true} {
		s := proseBarSize(loaded.build(), 80, 24)
		next, _ := s.Update(listRuneKey("r"))
		s = next.(proseBarScreen)
		if failed {
			s = proseLoadDeliver(s, proseLoadFailure(proseLoadScreens()[6], http.StatusBadGateway))
		}
		next, _ = s.Update(listRuneKey("x"))
		next, cmd := next.(proseBarScreen).Update(listRuneKey("y"))
		if cmd != nil {
			t.Errorf("failed=%t: x then y issued a delete command from a hidden load state", failed)
		}
		if next.(*CategoryListScreen).confirmingDelete {
			t.Errorf("failed=%t: x entered a hidden delete confirmation", failed)
		}
	}
}

func proseLoadActWords(changed, acted bool) string {
	switch {
	case changed && acted:
		return "changes the pane and issues a command that acts"
	case changed:
		return "changes what the operator sees"
	}
	return "issues a command that acts — a reload, a switch or a write — with nothing on " +
		"the bar saying the key does anything"
}

// proseLoadKeyEffect presses one key on a fresh build of the fixture and reports
// whether it changed the clipped pane, whether its command ACTED — produced a
// message other than a status toast, or reached for a backend — and whether it
// answered with a toast.
func proseLoadKeyEffect(f proseBarFixture, w, h int, key string) (changed, acted, toasted bool) {
	s := proseBarSize(f.build(), w, h)
	pane := func() string { return clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)) }
	before := pane()
	next, cmd := s.Update(listRuneKey(key))
	if out, ok := next.(proseBarScreen); ok {
		s = out
	}
	changed = pane() != before
	msgs, reached := proseLoadRun(cmd, 0)
	acted = reached
	for _, m := range msgs {
		if _, toast := m.(StatusMsg); toast {
			toasted = true
		} else {
			acted = true
		}
	}
	return changed, acted, toasted
}

// TestProseBar_ALoadFrameKeepsItsBarWhereverTheFailureHeadAndTheBarFit: on
// every load frame, at every pane Root draws, every segment of the bar reaches
// the operator whole wherever the pane holds ONE row of what the frame is about
// and the bar under it.
//
// NARROWER SCOPING THAN TheFooterSurvivesEveryDrawablePane'S, on purpose. That
// sweep asserts only where the whole frame FITS, which is right for a body that
// has nothing to give — and wrong here, where the frame has a stated give-order
// (proseRefusalFrame): the OMS body gives, then the standing note, and the bar
// never does. Scoped by "the frame fits", a note that pushed the bar off the pane
// simply made the frame not fit and was skipped: measured at 80x12 before the
// give-order existed, the analytics pulse's staff-only notice took the bar off
// entirely and that sweep passed. The boundary here is DERIVED from what the
// frame needs at the least — its lead row, the blank and the folded bar — and
// both sides of it must be reached. A screen whose load frames carry more than
// that states its own floor (proseLoadScreen.ownFloor) and is asked it.
func TestProseBar_ALoadFrameKeepsItsBarWhereverTheFailureHeadAndTheBarFit(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	forms := map[string]string{}
	for _, ls := range proseLoadScreens() {
		if ls.failureIsAForm != "" {
			forms[ls.name+"/"+string(proseLoadFailed)] = ls.failureIsAForm
		}
	}
	exempted := 0
	for _, f := range proseBarFixtures() {
		if f.load == "" {
			continue
		}
		if forms[f.name] != "" {
			exempted++
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			held, short := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					s := proseBarSize(f.build(), w, h)
					bar := s.proseBar()
					below := screenBodyRows(h) < 1+bar.rows(proseBarCells(w))
					if floored, ok := s.(proseLoadFloored); ok {
						// The screen's own give-order says where its floor is (see
						// proseLoadScreen.ownFloor). It is a FLOOR and not "the frame
						// fits": what gives above it — an OMS body — gives, so a frame
						// that pushed its bar off above the floor still fails here.
						below = !floored.frameFits()
					}
					if below {
						short++
						continue
					}
					held++
					pane := clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
					for _, seg := range bar {
						if !strings.Contains(stripANSI(pane), seg.Hint) {
							t.Errorf("at %dx%d the %s frame has room for its lead row and its bar, "+
								"and the pane does not carry %q whole.\nbar: %s\npane:\n%s",
								w, h, f.name, seg.Hint, bar.hint(), stripANSI(pane))
						}
					}
				}
			}
			if held == 0 || short == 0 {
				t.Errorf("the %s frame reached %d panes with room for its bar and %d without, "+
					"so one side of the boundary this sweep is scoped by was never tested",
					f.name, held, short)
			}
		})
	}
	if exempted != len(forms) {
		t.Errorf("proseLoadScreens exempts %d failure frames as forms and %d fixtures matched — "+
			"a stale exemption excuses a frame this sweep would otherwise check", len(forms), exempted)
	}
}
