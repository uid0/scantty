package omsapi

import (
	"context"
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
