package prreview

// CoverageGaps returns the clusters with no finding pointing at any of their
// files, and the high-exposure blast-radius files no finding points at
// either: the two things phase 6's coverage gate checks (docs/prds/
// pr-review-agent.md, phase 6).
func CoverageGaps(clusters []Cluster, highExposure []string, findings []Finding) (uncoveredClusters []Cluster, uncoveredFiles []string) {
	reviewed := make(map[string]bool, len(findings))
	for _, f := range findings {
		reviewed[f.File] = true
	}
	for _, cluster := range clusters {
		covered := false
		for _, file := range cluster.Files {
			if reviewed[file] {
				covered = true
				break
			}
		}
		if !covered {
			uncoveredClusters = append(uncoveredClusters, cluster)
		}
	}
	for _, file := range highExposure {
		if !reviewed[file] {
			uncoveredFiles = append(uncoveredFiles, file)
		}
	}
	return uncoveredClusters, uncoveredFiles
}

// ClusterFindings groups findings by the cluster whose files contain them,
// keyed by cluster ID. Phase 5's compound-defect check runs once per cluster
// with more than one confirmed finding, because a compound defect is a
// property of findings that share a place, not of the whole PR. A finding
// whose file belongs to no cluster contributes to none.
func ClusterFindings(clusters []Cluster, findings []Finding) map[string][]Finding {
	fileToCluster := make(map[string]string, len(findings))
	for _, cluster := range clusters {
		for _, file := range cluster.Files {
			fileToCluster[file] = cluster.ID
		}
	}
	grouped := make(map[string][]Finding)
	for _, f := range findings {
		id, ok := fileToCluster[f.File]
		if !ok {
			continue
		}
		grouped[id] = append(grouped[id], f)
	}
	return grouped
}
