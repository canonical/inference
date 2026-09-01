package providers

import (
	"context"
	"fmt"
	"sort"
)

type Registry interface {
	List(context.Context) ([]Definition, []string, error)
}

type StatusSource interface {
	Statuses(context.Context) (map[string]string, error)
}

type Service struct {
	Registry      Registry
	StatusSources map[Type]StatusSource
}

func NewService(registry Registry, statusSources map[Type]StatusSource) *Service {
	return &Service{Registry: registry, StatusSources: statusSources}
}

func (s *Service) List(ctx context.Context, installedOnly bool) ([]Provider, []string, error) {
	definitions, warnings, err := s.Registry.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("listing provider registry: %w", err)
	}

	statusesByType := make(map[Type]map[string]string)
	seenNames := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			return nil, nil, fmt.Errorf("provider registry returned a provider with no name")
		}
		if definition.Type == "" {
			return nil, nil, fmt.Errorf("provider registry returned provider %q with no type", definition.Name)
		}
		if seenNames[definition.Name] {
			return nil, nil, fmt.Errorf("provider registry returned duplicate provider %q", definition.Name)
		}
		seenNames[definition.Name] = true
	}

	for _, definition := range definitions {
		if _, resolved := statusesByType[definition.Type]; resolved {
			continue
		}
		source, ok := s.StatusSources[definition.Type]
		if !ok {
			return nil, nil, fmt.Errorf("no status source configured for provider type %q", definition.Type)
		}
		statuses, err := source.Statuses(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("reading statuses for provider type %q: %w", definition.Type, err)
		}
		statusesByType[definition.Type] = statuses
	}

	list := make([]Provider, 0, len(definitions))
	for _, definition := range definitions {
		status, installed := statusesByType[definition.Type][definition.Name]
		if installed && status == "" {
			return nil, nil, fmt.Errorf(
				"status source for provider type %q returned an empty status for %q",
				definition.Type,
				definition.Name,
			)
		}
		if !installed {
			status = StatusNotInstalled
		}
		if installedOnly && !installed {
			continue
		}
		list = append(list, Provider{
			Name:   definition.Name,
			Type:   definition.Type,
			Status: status,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name == list[j].Name {
			return list[i].Type < list[j].Type
		}
		return list[i].Name < list[j].Name
	})
	return list, warnings, nil
}
