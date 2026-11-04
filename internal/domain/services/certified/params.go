package certified

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"

	"github.com/forward-mcp/internal/domain/entities"
)

// BindParameters validates caller-supplied parameters against an entry's
// declared parameters, applies defaults, and rejects unknown names. The
// returned map is what is sent to Forward.
func BindParameters(q entities.CertifiedQuery, in map[string]any) (map[string]any, error) {
	declared := map[string]entities.QueryParameter{}
	for _, p := range q.Parameters {
		declared[p.Name] = p
	}
	var errs []error
	out := map[string]any{}
	for name := range in {
		if _, ok := declared[name]; !ok {
			errs = append(errs, fmt.Errorf("parameter %q is not declared by %s", name, q.ID))
		}
	}
	for _, p := range q.Parameters {
		v, present := in[p.Name]
		if !present || v == nil {
			if p.Required {
				errs = append(errs, fmt.Errorf("parameter %q (%s) is required", p.Name, p.Type))
				continue
			}
			if len(p.Default) > 0 {
				var d any
				_ = json.Unmarshal(p.Default, &d)
				out[p.Name] = d
			}
			continue
		}
		if err := checkType(p, v); err != nil {
			errs = append(errs, err)
			continue
		}
		out[p.Name] = v
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func checkType(p entities.QueryParameter, v any) error {
	bad := func(want string) error {
		return fmt.Errorf("parameter %q must be %s, got %T", p.Name, want, v)
	}
	switch p.Type {
	case entities.ParamString, entities.ParamDevice:
		if s, ok := v.(string); !ok || s == "" {
			return bad("a non-empty string")
		}
	case entities.ParamInt:
		switch n := v.(type) {
		case float64:
			if n != math.Trunc(n) {
				return bad("an integer")
			}
		case int, int32, int64:
		default:
			return bad("an integer")
		}
	case entities.ParamBool:
		if _, ok := v.(bool); !ok {
			return bad("a boolean")
		}
	case entities.ParamIP:
		s, ok := v.(string)
		if !ok || net.ParseIP(s) == nil {
			return bad("an IP address")
		}
	case entities.ParamCIDR:
		s, ok := v.(string)
		if !ok {
			return bad("a CIDR")
		}
		if _, _, err := net.ParseCIDR(s); err != nil {
			return bad("a CIDR like 10.0.0.0/24")
		}
	case entities.ParamList:
		if _, ok := v.([]any); !ok {
			return bad("a list")
		}
	default:
		return fmt.Errorf("parameter %q has unknown type %q", p.Name, p.Type)
	}
	return nil
}
