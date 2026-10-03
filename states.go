package walk

import "fmt"

// States returns the key of every reachable state, for inspecting a model by
// hand or diffing two versions of one. It walks the same way Check does but
// tests nothing.
func States(m *Model, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1 << 20
	}
	init := State(m.Init).clone()
	queue := []State{init}
	seen := map[string]bool{init.Key(): true}
	out := []string{init.Key()}

	for i := 0; i < len(queue); i++ {
		cur := queue[i]
		for _, a := range m.Actions {
			ok, err := m.eval(a.Guard, cur)
			if err != nil {
				return nil, fmt.Errorf("action %q: %w", a.Name, err)
			}
			if !ok {
				continue
			}
			next, err := m.apply(a, cur)
			if err != nil {
				return nil, fmt.Errorf("action %q: %w", a.Name, err)
			}
			key := next.Key()
			if seen[key] {
				continue
			}
			if len(out) >= limit {
				return out, fmt.Errorf("stopped after %d states; raise -limit to walk the whole model", limit)
			}
			seen[key] = true
			out = append(out, key)
			queue = append(queue, next)
		}
	}
	return out, nil
}
