package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return rdb, nil
}

// RankingKey returns the Redis key for a race's leaderboard ZSET.
func RankingKey(raceID string) string {
	return "race:" + raceID + ":ranking"
}

// FactionScoreKey returns the Redis key for faction scores.
func FactionScoreKey(raceID string) string {
	return "race:" + raceID + ":faction_score"
}

// SlotsKey returns the Redis key for available race slots counter.
func SlotsKey(raceID string) string {
	return "race:" + raceID + ":slots"
}

// PubSubChannel returns the Redis Pub/Sub channel for a race's real-time events.
func PubSubChannel(raceID string) string {
	return "pubsub:race:" + raceID
}

// RateLimitKey returns the Redis key for rate limiting a user action.
func RateLimitKey(userID, action string) string {
	return "ratelimit:" + userID + ":" + action
}
