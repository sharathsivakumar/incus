package instance

import (
	"github.com/lxc/incus/internal/filter"
	"github.com/lxc/incus/shared/api"
)

// FilterFull returns a filtered list of full instances that match the given clauses.
func FilterFull(instances []*api.InstanceFull, clauses filter.ClauseSet) ([]*api.InstanceFull, error) {
	filtered := []*api.InstanceFull{}
	for _, instance := range instances {
		match, err := filter.Match(*instance, clauses)
		if err != nil {
			return nil, err
		}

		if !match {
			continue
		}

		filtered = append(filtered, instance)
	}

	return filtered, nil
}
