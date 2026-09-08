package reasoning

import "errors"

// ResolveModelMapping follows configured aliases, including reasoning base
// aliases. Self-maps terminate; a cycle involving distinct models is rejected.
func ResolveModelMapping(mapping map[string]string, origin string) (string, bool, error) {
	current := origin
	visited := map[string]bool{current: true}
	for {
		next, exists := mapping[current]
		base := BaseModelName(current)
		if (!exists || next == "") && base != current {
			next, exists = mapping[base]
		}
		if !exists || next == "" {
			return current, current != origin, nil
		}
		if visited[next] {
			if next == current {
				return current, current != origin, nil
			}
			return "", false, errors.New("model_mapping_contains_cycle")
		}
		visited[next] = true
		current = next
	}
}
