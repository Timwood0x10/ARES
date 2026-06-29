package discovery

import "strings"

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
				if _, ok := mergedMeta[k]; !ok || r.Confidence >= best.Confidence {
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

	// Check source set.
	oldSources := make(map[string]bool, len(old.Records))
	for _, r := range old.Records {
		oldSources[r.Source] = true
	}
	for _, r := range new.Records {
		if !oldSources[r.Source] {
			return true
		}
	}

	// Check tags changed.
	if !stringSliceEqual(old.Identity.Tags, new.Identity.Tags) {
		return true
	}

	// Check metadata changed.
	if !stringMapEqual(old.Identity.Metadata, new.Identity.Metadata) {
		return true
	}

	return false
}

// stringSliceEqual compares two string slices as sets.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

// stringMapEqual compares two string maps.
func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// knownLaunchers are wrapper commands that launch MCP servers.
// For these, the identity includes the first argument (the actual server).
var knownLaunchers = map[string]bool{
	"uvx":  true,
	"npx":  true,
	"bunx": true,
	"pipx": true,
}

// normalizeEndpoint creates a stable key from an endpoint string.
// Uses the binary name (last path segment) as the key so that
// "codegraph" and "/usr/local/bin/codegraph" are merged.
// For known launchers (uvx, npx, etc.), includes the first argument.
func normalizeEndpoint(endpoint string) string {
	if endpoint == "" {
		return "unknown"
	}
	// Extract binary name from path.
	name := endpoint
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			name = name[i+1:]
			break
		}
	}
	if name == "" {
		return endpoint
	}
	// For known launchers, include the first argument.
	// "uvx blender-mcp" → "uvx blender-mcp"
	// "npx @modelcontextprotocol/server-filesystem" → "npx @modelcontextprotocol/server-filesystem"
	if idx := strings.IndexByte(name, ' '); idx >= 0 {
		launcher := name[:idx]
		if knownLaunchers[launcher] {
			return name
		}
		return launcher
	}
	return name
}

// extractName derives a human-readable name from a record.
func extractName(record DiscoveryRecord) string {
	return normalizeEndpoint(record.Endpoint)
}
