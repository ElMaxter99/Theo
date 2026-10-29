package engine

import "github.com/ElMaxter99/Theo/internal/compose"

// resolvedWith returns what `docker compose config` would print for the given
// services after Theo's generated file sets name=value in every one of them
// and removes their fixed container names: the fake Compose does not read the
// file, so tests state the result the checks look for.
func resolvedWith(services map[string]compose.Service, name, value string) *compose.Project {
	out := map[string]compose.Service{}
	for k, s := range services {
		s.ContainerName = ""
		if name != "" {
			env := map[string]*string{}
			for n, v := range s.Environment {
				env[n] = v
			}
			v := value
			env[name] = &v
			s.Environment = env
		}
		out[k] = s
	}
	return &compose.Project{Services: out}
}
