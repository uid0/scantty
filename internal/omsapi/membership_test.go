package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListUsers(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":42,"username":"ada","first_name":"Ada","last_name":"Lovelace"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	users, err := c.ListUsers(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if path != "/api/membership/users/" {
		t.Fatalf("path = %q", path)
	}
	if len(users.Results) != 1 || users.Results[0].ID != 42 || users.Results[0].Username != "ada" {
		t.Fatalf("unexpected users: %+v", users.Results)
	}
}

func TestListUsersAcceptsBareArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":7,"username":"grace"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	users, err := c.ListUsers(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListUsers bare array: %v", err)
	}
	if users.Count != 1 || len(users.Results) != 1 || users.Results[0].Username != "grace" {
		t.Fatalf("unexpected users: %+v count=%d", users.Results, users.Count)
	}
}

// TestListSIGMembersAcceptsBareArray guards the shape drift: SIGMemberViewSet
// is a plain ViewSet whose list() returns Response(serializer.data) — a BARE
// array with no pagination — so the old GetPage envelope decode crashed with
// `cannot unmarshal array into ... Page`. MaybeList must tolerate it.
func TestListSIGMembersAcceptsBareArray(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":7,"username":"grace","is_sig_admin":true},{"id":8,"username":"ada"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListSIGMembers(context.Background(), 5, nil)
	if err != nil {
		t.Fatalf("ListSIGMembers: %v", err)
	}
	if path != "/api/membership/sigs/5/members/" {
		t.Fatalf("path = %q", path)
	}
	if page.Count != 2 || len(page.Results) != 2 || !page.Results[0].IsSIGAdmin {
		t.Fatalf("unexpected members: %+v count=%d", page.Results, page.Count)
	}
}

// TestCreateSIG_Contract pins the SIG create body: POST to the sigs collection
// with name + group_email both present (no omitempty so a PATCH can clear the
// email), and the create response (id, name, group_email) decodes onto a SIG.
func TestCreateSIG_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":9,"name":"Woodshop","group_email":"wood@ex.org"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	sig, err := c.CreateSIG(context.Background(), SIGWrite{Name: "Woodshop", GroupEmail: "wood@ex.org"})
	if err != nil {
		t.Fatalf("CreateSIG: %v", err)
	}
	if sig == nil || sig.ID != 9 || sig.Name != "Woodshop" || sig.GroupEmail != "wood@ex.org" {
		t.Fatalf("unexpected sig: %+v", sig)
	}
	if captured.method != http.MethodPost || captured.path != "/api/membership/sigs/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Woodshop" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["group_email"] != "wood@ex.org" {
		t.Errorf("group_email = %v", captured.body["group_email"])
	}
}

// TestCreateSIG_BlankEmailStillSent confirms an empty group_email rides the wire
// as an empty string (no omitempty) so an edit that clears the email actually
// reaches the serializer's "clear the profile" path.
func TestCreateSIG_BlankEmailStillSent(t *testing.T) {
	var body map[string]any
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":10,"name":"Metal"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateSIG(context.Background(), SIGWrite{Name: "Metal"}); err != nil {
		t.Fatalf("CreateSIG: %v", err)
	}
	if v, present := body["group_email"]; !present || v != "" {
		t.Errorf("group_email = %v (present=%v), want empty string present; body=%s", v, present, raw)
	}
}

// TestUpdateSIG_Contract confirms edits go out as PATCH to the numeric detail URL.
func TestUpdateSIG_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"name":"Woodshop"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateSIG(context.Background(), 9, SIGWrite{Name: "Woodshop"}); err != nil {
		t.Fatalf("UpdateSIG: %v", err)
	}
	if method != http.MethodPatch || path != "/api/membership/sigs/9/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestDeleteSIG_Contract confirms DELETE to the numeric detail URL.
func TestDeleteSIG_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteSIG(context.Background(), 9); err != nil {
		t.Fatalf("DeleteSIG: %v", err)
	}
	if method != http.MethodDelete || path != "/api/membership/sigs/9/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestGetSIG_HydratesReadFields confirms the retrieve serializer's counts +
// admins decode onto the SIG.
func TestGetSIG_HydratesReadFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/membership/sigs/9/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"name":"Woodshop","group_email":"wood@ex.org","member_count":4,"asset_count":2,"inventory_count":1,"is_user_admin":true,"admins":[{"id":3,"username":"ada","email":"a@ex.org","handle":"ada"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	sig, err := c.GetSIG(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetSIG: %v", err)
	}
	if sig.MemberCount != 4 || sig.AssetCount != 2 || sig.InventoryCount != 1 || !sig.IsUserAdmin {
		t.Errorf("counts/flags not hydrated: %+v", sig)
	}
	if len(sig.Admins) != 1 || sig.Admins[0].Username != "ada" {
		t.Errorf("admins not hydrated: %+v", sig.Admins)
	}
}

// TestAddSIGMember_Contract pins the member-add call: POST to the nested members
// collection with a {"user_id": <id>} body.
func TestAddSIGMember_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42,"username":"ada"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.AddSIGMember(context.Background(), 9, 42); err != nil {
		t.Fatalf("AddSIGMember: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/membership/sigs/9/members/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["user_id"].(float64) != 42 {
		t.Errorf("user_id = %v (%T)", captured.body["user_id"], captured.body["user_id"])
	}
}

// TestRemoveSIGMember_Contract confirms the member-remove call DELETEs the
// nested detail URL whose pk is the USER id, and tolerates a 204.
func TestRemoveSIGMember_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.RemoveSIGMember(context.Background(), 9, 42); err != nil {
		t.Fatalf("RemoveSIGMember: %v", err)
	}
	if method != http.MethodDelete || path != "/api/membership/sigs/9/members/42/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestListAllUsers_PagesThrough confirms ListAllUsers follows the paginated
// envelope's `next` across pages and accumulates every user — so the member-add
// picker can reach a user who isn't on page 1.
func TestListAllUsers_PagesThrough(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "", "1":
			next := srv.URL + "/api/membership/users/?page=2"
			_, _ = w.Write([]byte(`{"count":2,"next":"` + next + `","previous":null,"results":[{"id":1,"username":"ada"}]}`))
		default:
			_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[{"id":2,"username":"grace"}]}`))
		}
	}))
	defer srv.Close()

	c := New(srv.URL)
	users, err := c.ListAllUsers(context.Background())
	if err != nil {
		t.Fatalf("ListAllUsers: %v", err)
	}
	if len(users) != 2 || users[0].Username != "ada" || users[1].Username != "grace" {
		t.Fatalf("unexpected users: %+v", users)
	}
}

// TestListAllUsers_FallsBackToAPIRoot confirms a 404 on the membership directory
// falls back to /api/users/ (older backends), mirroring ListUsers.
func TestListAllUsers_FallsBackToAPIRoot(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/membership/users/" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"missing"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":7,"username":"grace"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	users, err := c.ListAllUsers(context.Background())
	if err != nil {
		t.Fatalf("ListAllUsers fallback: %v", err)
	}
	if len(users) != 1 || users[0].Username != "grace" {
		t.Fatalf("unexpected users: %+v", users)
	}
	if len(paths) < 2 || paths[0] != "/api/membership/users/" || paths[len(paths)-1] != "/api/users/" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestListUsersFallsBackToAPIRoot(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/membership/users/" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"missing"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":7,"username":"grace"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	users, err := c.ListUsers(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListUsers fallback: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/api/membership/users/" || paths[1] != "/api/users/" {
		t.Fatalf("paths = %v", paths)
	}
	if len(users.Results) != 1 || users.Results[0].Username != "grace" {
		t.Fatalf("unexpected users: %+v", users.Results)
	}
}
