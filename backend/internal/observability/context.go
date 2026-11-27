package observability

import "context"

type actorKey struct{}

func WithActor(ctx context.Context, actor int) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

func Actor(ctx context.Context) int {
	actor, _ := ctx.Value(actorKey{}).(int)
	return actor
}
