package walk

import "fmt"

// eval reduces an expression to a boolean in the given state.
//
// A bare string is a variable reference when it names a declared variable and
// a value literal otherwise; Validate has already refused any model where that
// reading would be ambiguous. A nil expression is true, which lets an action
// omit its guard to mean "always enabled".
func (m *Model) eval(e Expr, s State) (bool, error) {
	if e == nil {
		return true, nil
	}
	switch v := e.(type) {
	case bool:
		return v, nil
	case string:
		// A bare term is only boolean when it is a variable holding "true".
		return m.term(v, s) == "true", nil
	case []any:
		op := v[0].(string)
		args := v[1:]
		switch op {
		case "==":
			l, err := m.operand(args[0], s)
			if err != nil {
				return false, err
			}
			r, err := m.operand(args[1], s)
			if err != nil {
				return false, err
			}
			return l == r, nil
		case "!=":
			l, err := m.operand(args[0], s)
			if err != nil {
				return false, err
			}
			r, err := m.operand(args[1], s)
			if err != nil {
				return false, err
			}
			return l != r, nil
		case "not":
			b, err := m.eval(args[0], s)
			if err != nil {
				return false, err
			}
			return !b, nil
		case "and":
			for _, a := range args {
				b, err := m.eval(a, s)
				if err != nil {
					return false, err
				}
				if !b {
					return false, nil
				}
			}
			return true, nil
		case "or":
			for _, a := range args {
				b, err := m.eval(a, s)
				if err != nil {
					return false, err
				}
				if b {
					return true, nil
				}
			}
			return false, nil
		case "in":
			l, err := m.operand(args[0], s)
			if err != nil {
				return false, err
			}
			for _, a := range args[1:] {
				r, err := m.operand(a, s)
				if err != nil {
					return false, err
				}
				if l == r {
					return true, nil
				}
			}
			return false, nil
		}
		return false, fmt.Errorf("unknown operator %q", op)
	}
	return false, fmt.Errorf("cannot evaluate %T", e)
}

// operand reduces one side of a comparison to the string it stands for.
func (m *Model) operand(e Expr, s State) (string, error) {
	switch v := e.(type) {
	case string:
		return m.term(v, s), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case []any:
		b, err := m.eval(v, s)
		if err != nil {
			return "", err
		}
		if b {
			return "true", nil
		}
		return "false", nil
	}
	return "", fmt.Errorf("cannot use %T as an operand", e)
}

// term resolves a bare string: a declared variable becomes its current value,
// anything else stands for itself.
func (m *Model) term(name string, s State) string {
	if _, ok := m.Vars[name]; ok {
		return s[name]
	}
	return name
}
