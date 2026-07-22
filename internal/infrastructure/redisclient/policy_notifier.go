package redisclient

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const policyVersionChannel = "campus:permission:policy-version"

// PolicyNotifier broadcasts permission policy versions over Redis.
type PolicyNotifier struct{ client *redis.Client }

// NewPolicyNotifier creates a Redis-backed permission notifier.
func NewPolicyNotifier(client *redis.Client) *PolicyNotifier { return &PolicyNotifier{client: client} }

// Publish announces one committed policy version.
func (n *PolicyNotifier) Publish(ctx context.Context, version string) error {
	return n.client.Publish(ctx, policyVersionChannel, version).Err()
}

// Run consumes versions until the context is cancelled. go-redis reconnects subscriptions internally.
func (n *PolicyNotifier) Run(ctx context.Context, consume func(string) error) error {
	subscription := n.client.Subscribe(ctx, policyVersionChannel)
	defer func() { _ = subscription.Close() }()
	if _, err := subscription.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe permission policy versions: %w", err)
	}
	for {
		message, err := subscription.ReceiveMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("receive permission policy version: %w", err)
		}
		if err = consume(message.Payload); err != nil {
			return err
		}
	}
}
