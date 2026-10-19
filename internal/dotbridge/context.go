package dotbridge

import "context"

type clientKey struct{}

func withClient(ctx context.Context, client ClientPolicy) context.Context {
	return context.WithValue(ctx, clientKey{}, client)
}

func clientFrom(ctx context.Context) ClientPolicy {
	if v, ok := ctx.Value(clientKey{}).(ClientPolicy); ok {
		return v
	}
	return ClientPolicy{}
}
