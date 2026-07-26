package valkey

import (
	"context"
	"fmt"
	"strconv"

	valkeygo "github.com/valkey-io/valkey-go"
)

const loadScript = `
local revision = redis.call('HGET', KEYS[1], 'revision')
if not revision then
  return nil
end
return {tonumber(revision), redis.call('HGET', KEYS[1], 'document')}
`

const compareAndSwapScript = `
local current = redis.call('HGET', KEYS[1], 'revision')
local expected = tonumber(ARGV[1])
if not current then
  if expected ~= 0 then
    return 0
  end
  redis.call('HSET', KEYS[1], 'revision', 1, 'document', ARGV[2])
  return 1
end
if tonumber(current) ~= expected then
  return 0
end
redis.call('HSET', KEYS[1], 'revision', expected + 1, 'document', ARGV[2])
return 1
`

type nativeExecutor interface {
	evalArray(context.Context, string, string, ...string) ([]valkeygo.ValkeyMessage, error)
	evalInt(context.Context, string, string, ...string) (int64, error)
	ping(context.Context) error
}

type commandExecutor struct{ client valkeygo.CommandClient }

func (executor commandExecutor) evalArray(
	ctx context.Context,
	script, key string,
	arguments ...string,
) ([]valkeygo.ValkeyMessage, error) {
	command := executor.client.B().Eval().Script(script).Numkeys(1).Key(key).
		Arg(arguments...).Build()
	return executor.client.Do(ctx, command).ToArray()
}

func (executor commandExecutor) evalInt(
	ctx context.Context,
	script, key string,
	arguments ...string,
) (int64, error) {
	command := executor.client.B().Eval().Script(script).Numkeys(1).Key(key).
		Arg(arguments...).Build()
	return executor.client.Do(ctx, command).ToInt64()
}

func (executor commandExecutor) ping(ctx context.Context) error {
	return executor.client.Do(ctx, executor.client.B().Ping().Build()).Error()
}

// NativeTransport adapts valkey-go without taking ownership of its lifecycle.
type NativeTransport struct{ executor nativeExecutor }

func NewNativeTransport(client valkeygo.CommandClient) *NativeTransport {
	return &NativeTransport{executor: commandExecutor{client: client}}
}

func (transport *NativeTransport) Load(
	ctx context.Context,
	key string,
) ([]byte, uint64, bool, error) {
	result, err := transport.executor.evalArray(ctx, loadScript, key)
	if valkeygo.IsValkeyNil(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if len(result) != 2 {
		return nil, 0, false, fmt.Errorf("valkey tenant state reply has %d elements", len(result))
	}
	revision, err := result[0].ToInt64()
	if err != nil || revision <= 0 {
		return nil, 0, false, fmt.Errorf("decode valkey tenant revision: %w", err)
	}
	data, err := result[1].AsBytes()
	if err != nil {
		return nil, 0, false, fmt.Errorf("decode valkey tenant document: %w", err)
	}

	return append([]byte(nil), data...), uint64(revision), true, nil
}

func (transport *NativeTransport) CompareAndSwap(
	ctx context.Context,
	key string,
	expectedRevision uint64,
	data []byte,
) (bool, error) {
	result, err := transport.executor.evalInt(
		ctx, compareAndSwapScript, key, strconv.FormatUint(expectedRevision, 10), string(data),
	)
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (transport *NativeTransport) Ping(ctx context.Context) error {
	return transport.executor.ping(ctx)
}
