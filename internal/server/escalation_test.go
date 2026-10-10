package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"

	"convia/internal/operator"
)

/*
TestNoOperatorScopeStandsInForAnother is `M21-010`, the privilege escalation
test, run over the whole operator surface rather than a hand-picked part of it.

For every operator route it reads the scope the contract says the route needs,
and presents a key carrying **every other scope Convia has** -- including
`operators:write`, the one that mints keys, and `audit:read`, the one bounded by
nothing. If any route were reachable by a scope that is not its own, that key
would reach it. A route added later is held to this without anybody adding it
here, and a route whose contract names no scope fails rather than passing for
want of one to remove.

The request carries a reason and a confirmation, so that what refuses it is the
scope and not the questions asked before it.
*/
func TestNoOperatorScopeStandsInForAnother(t *testing.T) {
	document := loadSpecification(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	checked := 0
	for _, entry := range routeTable(logger, testDependencies()) {
		if entry.surface != surfaceOperator {
			continue
		}

		operation := document.Paths.Find(entry.path).GetOperation(entry.method)
		var required []string
		if operation != nil && operation.Security != nil {
			for _, requirement := range *operation.Security {
				required = append(required, requirement["OperatorKey"]...)
			}
		}
		if len(required) != 1 {
			t.Errorf("%s %s: the contract names %v, want exactly one operator scope", entry.method, entry.path, required)
			continue
		}

		var others []operator.Scope
		for _, scope := range operator.Scopes() {
			if string(scope) != required[0] {
				others = append(others, scope)
			}
		}

		dependencies := testDependencies()
		dependencies.OperatorAuthenticator = stubOperatorAuthenticator{principal: operator.Principal{
			CredentialID: "oper_4XZQP7KN2VJH6TBWMDR3YAFC5E", Scopes: others}}
		handler := New("127.0.0.1:0", logger, dependencies).Handler

		target := concrete(entry.path)
		var request *http.Request
		switch entry.method {
		case http.MethodPost, http.MethodPatch, http.MethodPut:
			request = jsonRequest(entry.method, target, requestBodyFor(entry.path))
		default:
			request = httptest.NewRequest(entry.method, target, nil)
		}
		request = asOperator(request)
		request.Header.Set(confirmHeader, path.Base(target))

		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		checked++

		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s needs %s, and a key with every other scope got %d: %s",
				entry.method, entry.path, required[0], response.Code, strings.TrimSpace(response.Body.String()))
		}
	}

	if checked < 30 {
		t.Errorf("only %d operator routes were checked", checked)
	}
}

/*
requestBodyFor is a body each write would accept, so that what decides the
answer is the scope rather than a malformed request refused before it.
*/
func requestBodyFor(pattern string) string {
	switch {
	case strings.HasSuffix(pattern, "/operator/credentials"):
		return `{"name":"escalation","scopes":["applications:read"]}`
	case strings.HasSuffix(pattern, "/credentials"):
		return `{"name":"escalation","scopes":["users:read"]}`
	case strings.HasSuffix(pattern, "/users"):
		return `{"external_subject":"escalation","display_name":"Escalation"}`
	case strings.HasSuffix(pattern, "/rooms"):
		return `{"name":"Escalation"}`
	case strings.HasSuffix(pattern, "/applications"):
		return `{"name":"Escalation"}`
	case path.Base(pattern) == "{user_id}":
		return `{"display_name":"Escalation"}`
	case slices.Contains([]string{"{room_id}", "{application_id}"}, path.Base(pattern)):
		return `{"name":"Escalation"}`
	default:
		return `{}`
	}
}
