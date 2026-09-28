package prreview

import "testing"

func TestCoverageGapsFindsUncoveredClustersAndFiles(t *testing.T) {
	t.Parallel()

	clusters := []Cluster{
		{ID: "cluster_0", Name: "internal/a", Files: []string{"internal/a/a.go"}},
		{ID: "cluster_1", Name: "internal/b", Files: []string{"internal/b/b.go"}},
	}
	highExposure := []string{"internal/c/c.go", "internal/d/d.go"}
	findings := []Finding{
		{File: "internal/a/a.go"},
		{File: "internal/d/d.go"},
	}

	uncoveredClusters, uncoveredFiles := CoverageGaps(clusters, highExposure, findings)

	if len(uncoveredClusters) != 1 || uncoveredClusters[0].ID != "cluster_1" {
		t.Errorf("uncoveredClusters = %+v, want only cluster_1", uncoveredClusters)
	}
	if len(uncoveredFiles) != 1 || uncoveredFiles[0] != "internal/c/c.go" {
		t.Errorf("uncoveredFiles = %v, want only internal/c/c.go", uncoveredFiles)
	}
}

func TestCoverageGapsReportsNothingWhenFullyCovered(t *testing.T) {
	t.Parallel()

	clusters := []Cluster{{ID: "cluster_0", Files: []string{"a.go"}}}
	highExposure := []string{"b.go"}
	findings := []Finding{{File: "a.go"}, {File: "b.go"}}

	uncoveredClusters, uncoveredFiles := CoverageGaps(clusters, highExposure, findings)
	if uncoveredClusters != nil || uncoveredFiles != nil {
		t.Errorf("want no gaps, got clusters=%v files=%v", uncoveredClusters, uncoveredFiles)
	}
}

func TestClusterFindingsGroupsByFileMembership(t *testing.T) {
	t.Parallel()

	clusters := []Cluster{
		{ID: "cluster_0", Files: []string{"a.go", "a_test.go"}},
		{ID: "cluster_1", Files: []string{"b.go"}},
	}
	findings := []Finding{
		{File: "a.go", Title: "f1"},
		{File: "a_test.go", Title: "f2"},
		{File: "b.go", Title: "f3"},
		{File: "unrelated.go", Title: "f4"},
	}

	grouped := ClusterFindings(clusters, findings)

	if len(grouped["cluster_0"]) != 2 {
		t.Errorf("cluster_0 findings = %d, want 2", len(grouped["cluster_0"]))
	}
	if len(grouped["cluster_1"]) != 1 {
		t.Errorf("cluster_1 findings = %d, want 1", len(grouped["cluster_1"]))
	}
	if _, ok := grouped["cluster_2"]; ok {
		t.Error("no finding belongs to a cluster_2 that doesn't exist")
	}
}
