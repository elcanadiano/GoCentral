package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"rb3server/database"
)

// env variable names for each job interval
const (
	cleanupDuplicateScoresIntervalEnv           = "HOUSEKEEPING_CLEANUP_DUPLICATE_SCORES_INTERVAL"
	pruneOldSessionsIntervalEnv                 = "HOUSEKEEPING_PRUNE_OLD_SESSIONS_INTERVAL"
	cleanupInvalidScoresIntervalEnv             = "HOUSEKEEPING_CLEANUP_INVALID_SCORES_INTERVAL"
	deleteExpiredBattlesIntervalEnv             = "HOUSEKEEPING_DELETE_EXPIRED_BATTLES_INTERVAL"
	cleanupBannedUserScoresIntervalEnv          = "HOUSEKEEPING_CLEANUP_BANNED_USER_SCORES_INTERVAL"
	cleanupBannedUserAccomplishmentsIntervalEnv = "HOUSEKEEPING_CLEANUP_BANNED_USER_ACCOMPLISHMENTS_INTERVAL"
	cleanupInvalidUsersIntervalEnv              = "HOUSEKEEPING_CLEANUP_INVALID_USERS_INTERVAL"
)

type housekeepingJob struct {
	name     string
	interval time.Duration
	run      func()
}

func parseHousekeepingInterval(value string, defaultInterval time.Duration) (time.Duration, error) {
	if value == "" {
		return defaultInterval, nil
	}

	interval, err := time.ParseDuration(value)
	if err != nil {
		return defaultInterval, fmt.Errorf("invalid duration %q: %w", value, err)
	}
	if interval <= 0 {
		return defaultInterval, fmt.Errorf("duration must be greater than zero")
	}

	return interval, nil
}

func loadHousekeepingInterval(envName string, defaultInterval time.Duration) time.Duration {
	interval, err := parseHousekeepingInterval(os.Getenv(envName), defaultInterval)
	if err != nil {
		log.Printf("Invalid %s value; using default cadence %s: %v", envName, defaultInterval, err)
	}
	return interval
}

func configuredHousekeepingJobs() []housekeepingJob {
	return []housekeepingJob{
		{
			name:     "cleanup duplicate scores",
			interval: loadHousekeepingInterval(cleanupDuplicateScoresIntervalEnv, 24*time.Hour),
			run:      database.CleanupDuplicateScores,
		},
		{
			name:     "prune old sessions",
			interval: loadHousekeepingInterval(pruneOldSessionsIntervalEnv, 5*time.Minute),
			run:      database.PruneOldSessions,
		},
		{
			name:     "cleanup invalid scores",
			interval: loadHousekeepingInterval(cleanupInvalidScoresIntervalEnv, 24*time.Hour),
			run:      database.CleanupInvalidScores,
		},
		{
			name:     "delete expired battles",
			interval: loadHousekeepingInterval(deleteExpiredBattlesIntervalEnv, 15*time.Minute),
			run:      database.DeleteExpiredBattles,
		},
		{
			name:     "cleanup banned user scores",
			interval: loadHousekeepingInterval(cleanupBannedUserScoresIntervalEnv, 24*time.Hour),
			run:      database.CleanupBannedUserScores,
		},
		{
			name:     "cleanup banned user accomplishments",
			interval: loadHousekeepingInterval(cleanupBannedUserAccomplishmentsIntervalEnv, 24*time.Hour),
			run:      database.CleanupBannedUserAccomplishments,
		},
		{
			name:     "cleanup invalid users",
			interval: loadHousekeepingInterval(cleanupInvalidUsersIntervalEnv, 24*time.Hour),
			run:      database.CleanupInvalidUsers,
		},
	}
}

// nextHousekeepingRun advances a job past any intervals missed while another
// housekeeping job was in the middle of running. This prevents potentially expensive jobs from piling up
// and then running repeatedly without a pause
func nextHousekeepingRun(scheduledAt time.Time, interval time.Duration, completedAt time.Time) time.Time {
	if completedAt.Before(scheduledAt) {
		return scheduledAt
	}

	missedIntervals := completedAt.Sub(scheduledAt)/interval + 1
	return scheduledAt.Add(missedIntervals * interval)
}

// runHousekeepingScheduler keeps all housekeeping work serialized while giving each job independent timings
// basically this allows each job to be configured to run on a different basis
// the previous way was 1 minute for every job but that sucked since not all jobs needed to run that often
func runHousekeepingScheduler(quit <-chan struct{}, jobs []housekeepingJob) {
	if len(jobs) == 0 {
		<-quit
		return
	}

	nextRuns := make([]time.Time, len(jobs))
	startedAt := time.Now()
	for i, job := range jobs {
		nextRuns[i] = startedAt.Add(job.interval)
		log.Printf("Scheduled housekeeping job %q every %s", job.name, job.interval)
	}

	for {
		nextDeadline := nextRuns[0]
		for _, deadline := range nextRuns[1:] {
			if deadline.Before(nextDeadline) {
				nextDeadline = deadline
			}
		}

		wait := time.Until(nextDeadline)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)

		select {
		case <-quit:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case now := <-timer.C:
			for i, job := range jobs {
				if nextRuns[i].After(now) {
					continue
				}

				job.run()
				nextRuns[i] = nextHousekeepingRun(nextRuns[i], job.interval, time.Now())
			}
		}
	}
}
