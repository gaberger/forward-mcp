package entities

// Caller is the identity on whose behalf a tool call runs. In stdio mode it
// is always LocalCaller. In remote mode it is populated from the verified
// JWT by the identity adapter (M1c). Everything that must be partitioned per
// identity (cache, memory graph, bloom indexes, audit log) keys on Subject.
type Caller struct {
	// Subject is the stable identity string (JWT "sub"). Never empty.
	Subject string
	// Issuer is who vouched for the identity (JWT "iss"); empty for local.
	Issuer string
	// NetworkIDs, when non-empty, restricts the caller to these networks.
	NetworkIDs []string
	// Scopes are coarse capabilities granted to the caller, e.g. "predict".
	Scopes []string
}

// LocalSubject is the subject used when no authenticated caller exists
// (stdio mode, tests, CLI subcommands).
const LocalSubject = "local"

// LocalCaller is the identity used in stdio mode.
func LocalCaller() Caller {
	return Caller{Subject: LocalSubject}
}

// HasScope reports whether the caller holds the named scope.
func (c Caller) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// MayAccessNetwork reports whether the caller is allowed to act on the
// network. An empty NetworkIDs list means no restriction.
func (c Caller) MayAccessNetwork(networkID string) bool {
	if len(c.NetworkIDs) == 0 {
		return true
	}
	for _, id := range c.NetworkIDs {
		if id == networkID {
			return true
		}
	}
	return false
}

// ScopePredict must be present on a caller for propose-class tools to be
// callable, in addition to the server-side feature flag (PLAN.md D3).
const ScopePredict = "predict"
