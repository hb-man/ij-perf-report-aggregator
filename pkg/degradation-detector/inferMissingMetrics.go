package degradation_detector

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

type MissingData struct {
	LastBuild     string
	LastTimestamp int64
	TCBuildType   string
	Settings      Settings
}

const (
	DaysToCheckMissing     = -3
	StaleDataThresholdDays = -30 // Don't report if data has been missing longer than this
)

func InferMissingData(data <-chan QueryResultWithSettings) <-chan MissingData {
	output := make(chan MissingData, 100)
	go func() {
		for datum := range data {
			slog.Info("infer missing data", "settings", datum.Settings)
			for _, missingData := range inferMissingData(datum.builds, datum.timestamps, datum.buildTypes, datum.Settings) {
				output <- missingData
			}
		}
		close(output)
	}()
	return output
}

func inferMissingData(builds []string, timestamps []int64, buildTypes []string, analysisSettings Settings) []MissingData {
	result := make([]MissingData, 0)

	daysAgo := time.Now().AddDate(0, 0, analysisSettings.GetDaysToCheckMissing()).UnixMilli()

	monthAgo := time.Now().AddDate(0, 0, StaleDataThresholdDays).UnixMilli()

	lastTimestamp := timestamps[len(timestamps)-1]
	// if there the data is missing for the specified number of days but existed a month ago - report it
	if lastTimestamp < daysAgo && lastTimestamp > monthAgo {
		result = append(result, MissingData{
			LastBuild:     builds[len(builds)-1],
			LastTimestamp: lastTimestamp,
			Settings:      analysisSettings,
			TCBuildType:   buildTypes[len(buildTypes)-1],
		})
	}

	return result
}

type metricsMerger interface {
	MergeMetrics(settings Settings) Settings
}

func mergeMetricsHelper(settings Settings, newSettings Settings) Settings {
	switch s := settings.(type) {
	case FleetStartupSettings:
		s.Metric = fmt.Sprintf("%s, %s", s.Metric, newSettings.GetMetric())
		return s
	case PerformanceSettings:
		s.Metric = fmt.Sprintf("%s, %s", s.Metric, newSettings.GetMetric())
		return s
	case StartupSettings:
		s.Metric = fmt.Sprintf("%s, %s", s.Metric, newSettings.GetMetric())
		return s
	default:
		return settings
	}
}

func (s FleetStartupSettings) MergeMetrics(settings Settings) Settings {
	return mergeMetricsHelper(s, settings)
}

func (s PerformanceSettings) MergeMetrics(settings Settings) Settings {
	return mergeMetricsHelper(s, settings)
}

func (s StartupSettings) MergeMetrics(settings Settings) Settings {
	return mergeMetricsHelper(s, settings)
}

// slack channel => tc_build_type => project => missingData
type MissingDataMerged map[string]map[string]map[string]MissingData

func MergeMissingData(missingData <-chan MissingData) MissingDataMerged {
	// map of tc_build_type to project to missingData
	missingDataMerged := make(map[string]map[string]map[string][]MissingData)

	for datum := range missingData {
		slackChannel := datum.Settings.SlackChannel()
		if missingDataMerged[slackChannel] == nil {
			missingDataMerged[slackChannel] = make(map[string]map[string][]MissingData)
		}
		buildType := datum.TCBuildType
		if missingDataMerged[slackChannel][buildType] == nil {
			missingDataMerged[slackChannel][buildType] = make(map[string][]MissingData)
		}
		project := datum.Settings.GetProject()
		missingDataMerged[slackChannel][buildType][project] = append(
			missingDataMerged[slackChannel][buildType][project],
			datum,
		)
	}

	result := make(MissingDataMerged)
	for slackChannel, slackMissingData := range missingDataMerged {
		result[slackChannel] = make(map[string]map[string]MissingData)
		for buildType, buildMissingData := range slackMissingData {
			result[slackChannel][buildType] = make(map[string]MissingData)

			for project, projectMissingData := range buildMissingData {
				if len(projectMissingData) == 0 {
					continue
				}

				// Merge metrics within this project
				baseData := projectMissingData[0]
				for i := 1; i < len(projectMissingData); i++ {
					baseData.Settings = baseData.Settings.MergeMetrics(projectMissingData[i].Settings)
				}

				result[slackChannel][buildType][project] = baseData
			}
		}
	}

	return result
}

type GroupKey struct {
	Metrics       string
	LastTimestamp int64
}

func normalizeMetrics(metrics string) string {
	parts := strings.Split(metrics, ", ")
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func (d MissingData) GetRangeStartTime() time.Time {
	return time.Now().AddDate(0, -1, 0)
}

func GenerateMissingDataMessages(data MissingDataMerged) map[string]string {
	// Messages grouped by Slack channel
	channelMessages := make(map[string][]string)

	// Default to 3 if no settings provided is available
	channelDaysToCheck := make(map[string]int)

	// First, group all messages by Slack channel
	for slackChannel, buildTypeMap := range data {
		// Group projects by metrics and timestamp within each build type

		daysToCheckMissing := 3

		for buildType, projectMap := range buildTypeMap {
			// Create groups for this build type
			groups := make(map[GroupKey][]string)
			missingDataMap := make(map[GroupKey]MissingData)

			// Group projects by metrics and timestamp
			for project, missingData := range projectMap {
				daysValue := missingData.Settings.GetDaysToCheckMissing()
				if daysValue < 0 {
					daysToCheckMissing = -daysValue
				}

				key := GroupKey{
					Metrics:       normalizeMetrics(missingData.Settings.GetMetric()),
					LastTimestamp: missingData.LastTimestamp,
				}
				groups[key] = append(groups[key], project)
				missingDataMap[key] = missingData
			}

			// Build message for this build type with grouped projects
			var message strings.Builder

			// Create messages for each group
			for key, projects := range groups {
				readableDate := time.UnixMilli(key.LastTimestamp).Format("02-01-2006")

				// Sort projects for consistent output
				sort.Strings(projects)

				message.WriteString("*Projects:* ")
				message.WriteString(strings.Join(projects, ", "))
				message.WriteString("\nMetrics: ")
				message.WriteString(key.Metrics)
				message.WriteString("\nLast Recorded: ")
				message.WriteString(readableDate)

				missingData := missingDataMap[key]
				ijPerfLink := missingData.Settings.ChartLink(missingData)
				message.WriteString("\n" + ijPerfLink)
				message.WriteString("\n\n")
			}

			// Add build configuration link
			message.WriteString(fmt.Sprintf("<https://buildserver.labs.intellij.net/buildConfiguration/%s|TC Configuration>\n", buildType))

			channelMessages[slackChannel] = append(channelMessages[slackChannel], message.String())
			channelDaysToCheck[slackChannel] = daysToCheckMissing
		}
	}

	// Combine messages for each channel
	result := make(map[string]string)
	for channel, messages := range channelMessages {
		daysValue := channelDaysToCheck[channel]
		result[channel] = fmt.Sprintf("Data is missing for more than %d days:\n%s", daysValue, strings.Join(messages, "\n"))
	}

	return result
}

func SendMissingDataMessages(data MissingDataMerged, client *http.Client) {
	messages := GenerateMissingDataMessages(data)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for channel, message := range messages {
		err := SendSlackMessage(ctx, client, SlackMessage{
			Text:    message,
			Channel: channel,
		})
		if err != nil {
			slog.Error("failed to send slack message", "error", err)
		}
	}
}
