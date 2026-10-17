package identity

// Principal is constructed by host identity services, never from command bodies.
type Principal struct {
	ID       string   `json:"id"`
	OwnerID  string   `json:"ownerId"`
	ClientID string   `json:"clientId"`
	Role     string   `json:"role"`
	Scopes   []string `json:"scopes"`
}

func (p Principal) HasScope(scope string) bool {
	for _, allowed := range p.Scopes {
		if allowed == scope {
			return true
		}
	}
	return false
}

func OwnerScopes() []string {
	return []string{"canvas.read", "canvas.write", "resource.read", "resource.write", "generation.submit", "settings.write", "share.manage"}
}
