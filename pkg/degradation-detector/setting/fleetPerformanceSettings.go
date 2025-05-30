package setting

import (
	"log/slog"
	"net/http"
	"strings"

	detector "github.com/JetBrains/ij-perf-report-aggregator/pkg/degradation-detector"
)

func GenerateFleetPerformanceSettings(backendUrl string, client *http.Client) []detector.PerformanceSettings {
	settings := make([]detector.PerformanceSettings, 0, 100)
	mainSettings := detector.PerformanceSettings{
		Db:    "fleet",
		Table: "measure_new",
		BaseSettings: detector.BaseSettings{
			Branch:  "master",
			Machine: "intellij-linux-hw-hetzner%",
		},
	}
	slackSettings := detector.SlackSettings{
		Channel:     "fleet-performance-tests-notifications",
		ProductLink: "fleet",
	}

	tests, err := detector.FetchAllTests(backendUrl, client, mainSettings)
	if err != nil {
		slog.Error("error while getting tests", "error", err)
		return settings
	}
	var filteredTests []string
	for _, test := range tests {
		if test != "" && !strings.Contains(test, "%20") && !strings.Contains(test, "agent-") {
			filteredTests = append(filteredTests, test)
		}
	}

	metrics := []string{"fleet.test", "p99"}

	for _, test := range filteredTests {
		for _, metric := range metrics {
			settings = append(settings, detector.PerformanceSettings{
				Db:      mainSettings.Db,
				Table:   mainSettings.Table,
				Project: test,
				BaseSettings: detector.BaseSettings{
					Branch:  mainSettings.Branch,
					Machine: mainSettings.Machine,
					Metric:  metric,
					AnalysisSettings: detector.AnalysisSettings{
						MinimumSegmentLength:      15,
						MedianDifferenceThreshold: 5,
						EffectSizeThreshold:       2,
						ReportType:                detector.AllEvent,
					},
					SlackSettings: slackSettings,
				},
			})
		}
	}

	return settings
}
