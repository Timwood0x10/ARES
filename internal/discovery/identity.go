package discovery

// mergeRecords groups discovery records by endpoint and merges them into services.
func mergeRecords(records []DiscoveryRecord) map[string]*DiscoveredService {
	groups := make(map[string][]DiscoveryRecord)
	for _, r := range records {
		key := normalizeEndpoint(r.Endpoint)
		groups[key] = append(groups[key], r)
	}

	services := make(map[string]*DiscoveredService, len(groups))
	for key, group := range groups {
		svc := &DiscoveredService{
			Identity: ServiceIdentity{
				ID:   key,
				Name: extractName(group[0]),
				Type: ServiceTypeMCP,
			},
			Records: group,
		}
		best := group[0]
		for _, r := range group[1:] {
			if r.Confidence > best.Confidence {
				best = r
			}
		}
		svc.BestSource = best.Source

		// Collect tags from all records.
		tagSet := make(map[string]bool)
		for _, r := range group {
			for _, tag := range r.Tags {
				tagSet[tag] = true
			}
		}
		if len(tagSet) > 0 {
			tags := make([]string, 0, len(tagSet))
			for tag := range tagSet {
				tags = append(tags, tag)
			}
			svc.Identity.Tags = tags
		}

		// Merge metadata from all records (highest confidence wins on conflict).
		mergedMeta := make(map[string]string)
		for _, r := range group {
			for k, v := range r.Metadata {
				if existing, ok := mergedMeta[k]; !ok || r.Confidence >= best.Confidence {
					_ = existing
					mergedMeta[k] = v
				}
			}
		}
		if len(mergedMeta) > 0 {
			svc.Identity.Metadata = mergedMeta
		}

		services[key] = svc
	}

	return services
}

// diffServices compares new services against existing ones.
func diffServices(
	existing map[string]*DiscoveredService,
	newServices map[string]*DiscoveredService,
) (added, updated, removed []string) {
	for id, newSvc := range newServices {
		oldSvc, exists := existing[id]
		if !exists {
			added = append(added, id)
		} else if hasChanged(oldSvc, newSvc) {
			updated = append(updated, id)
		}
	}
	for id := range existing {
		if _, found := newServices[id]; !found {
			removed = append(removed, id)
		}
	}
	return added, updated, removed
}

// hasChanged checks if a service has meaningfully changed.
func hasChanged(old, new *DiscoveredService) bool {
	if len(old.Records) != len(new.Records) {
		return true
	}
	if old.BestSource != new.BestSource {
		return true
	}
	oldSources := make(map[string]bool, len(old.Records))
	for _, r := range old.Records {
		oldSources[r.Source] = true
	}
	for _, r := range new.Records {
		if !oldSources[r.Source] {
			return true
		}
	}
	return false
}

// normalizeEndpoint creates a stable key from an endpoint string.
func normalizeEndpoint(endpoint string) string {
	if endpoint == "" {
		return "unknown"
	}
	return endpoint
}

// extractName derives a human-readable name from a record.
func extractName(record DiscoveryRecord) string {
	if record.Endpoint == "" {
		return "unknown"
	}
	name := record.Endpoint
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			name = name[i+1:]
			break
		}
	}
	return name
}
