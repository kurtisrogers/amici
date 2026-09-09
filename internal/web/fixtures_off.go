//go:build !fixtures

package web

import "net/http"

// The default build has no fixture endpoints, and no code capable of serving
// them.
//
// This file is the whole of the release build's fixture surface: a function
// that registers nothing. There is no handler to reach, no reachable path that
// wipes a database, and nothing that will read a development outbox back to a
// caller, regardless of what AMICI_ENABLE_FIXTURES is set to.
//
// cmd/amici warns when the flag is set in a binary built this way, because a
// setting that silently does nothing is how somebody spends an afternoon
// wondering why the browser suite cannot reset anything.
func (s *Server) registerFixtureRoutes(*http.ServeMux, []middleware) {}
