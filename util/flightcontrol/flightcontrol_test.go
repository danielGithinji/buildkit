package flightcontrol

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestNoCancel(t *testing.T) {
	t.Parallel()
	g := &Group[string]{}
	eg, ctx := errgroup.WithContext(context.Background())
	var r1, r2 string
	var counter int64
	f := testFunc(100*time.Millisecond, "bar", &counter)
	eg.Go(func() error {
		ret1, err := g.Do(ctx, "foo", f)
		if err != nil {
			return err
		}
		r1 = ret1
		return nil
	})
	eg.Go(func() error {
		ret2, err := g.Do(ctx, "foo", f)
		if err != nil {
			return err
		}
		r2 = ret2
		return nil
	})
	err := eg.Wait()
	require.NoError(t, err)
	assert.Equal(t, "bar", r1)
	assert.Equal(t, "bar", r2)
	assert.Equal(t, int64(1), counter)
}

func TestCancelOne(t *testing.T) {
	t.Parallel()
	g := &Group[string]{}
	eg, ctx := errgroup.WithContext(context.Background())
	var r1, r2 string
	var counter int64
	f := testFunc(100*time.Millisecond, "bar", &counter)
	ctx2, cancel := context.WithCancelCause(ctx)
	eg.Go(func() error {
		ret1, err := g.Do(ctx2, "foo", f)
		require.Error(t, err)
		require.Equal(t, true, errors.Is(err, context.Canceled))
		if err == nil {
			r1 = ret1
		}
		return nil
	})
	eg.Go(func() error {
		ret2, err := g.Do(ctx, "foo", f)
		if err != nil {
			return err
		}
		r2 = ret2
		return nil
	})
	eg.Go(func() error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(30 * time.Millisecond):
			cancel(errors.WithStack(context.Canceled))
			return nil
		}
	})
	err := eg.Wait()
	require.NoError(t, err)
	assert.Equal(t, "", r1)
	assert.Equal(t, "bar", r2)
	assert.Equal(t, int64(1), counter)
}

func TestCancelRace(t *testing.T) {
	// t.Parallel() // disabled for better timing consistency. works with parallel as well

	g := &Group[struct{}]{}
	ctx, cancel := context.WithCancelCause(context.Background())

	kick := make(chan struct{})
	wait := make(chan struct{})

	count := 0

	// first run cancels context, second returns cleanly
	f := func(ctx context.Context) (struct{}, error) {
		done := ctx.Done()
		if count > 0 {
			time.Sleep(100 * time.Millisecond)
			return struct{}{}, nil
		}
		go func() {
			for {
				select {
				case <-wait:
					return
				default:
					ctx.Done()
				}
			}
		}()
		count++
		time.Sleep(50 * time.Millisecond)
		close(kick)
		time.Sleep(50 * time.Millisecond)
		select {
		case <-done:
			return struct{}{}, context.Cause(ctx)
		case <-time.After(200 * time.Millisecond):
		}
		return struct{}{}, nil
	}

	go func() {
		defer close(wait)
		<-kick
		cancel(errors.WithStack(context.Canceled))
		time.Sleep(5 * time.Millisecond)
		_, err := g.Do(context.Background(), "foo", f)
		assert.NoError(t, err)
	}()

	_, err := g.Do(ctx, "foo", f)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, context.Canceled))
	<-wait
}

func TestCancelBoth(t *testing.T) {
	t.Parallel()
	g := &Group[string]{}
	eg, ctx := errgroup.WithContext(context.Background())
	var r1, r2 string
	var counter int64
	f := testFunc(200*time.Millisecond, "bar", &counter)
	ctx2, cancel2 := context.WithCancelCause(ctx)
	ctx3, cancel3 := context.WithCancelCause(ctx)
	eg.Go(func() error {
		ret1, err := g.Do(ctx2, "foo", f)
		require.Error(t, err)
		require.Equal(t, true, errors.Is(err, context.Canceled))
		if err == nil {
			r1 = ret1
		}
		return nil
	})
	eg.Go(func() error {
		ret2, err := g.Do(ctx3, "foo", f)
		require.Error(t, err)
		require.Equal(t, true, errors.Is(err, context.Canceled))
		if err == nil {
			r2 = ret2
		}
		return nil
	})
	eg.Go(func() error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(20 * time.Millisecond):
			cancel2(errors.WithStack(context.Canceled))
			return nil
		}
	})
	eg.Go(func() error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(50 * time.Millisecond):
			cancel3(errors.WithStack(context.Canceled))
			return nil
		}
	})
	err := eg.Wait()
	require.NoError(t, err)
	assert.Equal(t, "", r1)
	assert.Equal(t, "", r2)
	assert.Equal(t, int64(1), counter)
	ret1, err := g.Do(context.TODO(), "foo", f)
	require.NoError(t, err)
	assert.Equal(t, "bar", ret1)

	ret1, err = g.Do(context.TODO(), "abc", f)
	require.NoError(t, err)
	assert.Equal(t, "bar", ret1)

	assert.Equal(t, int64(3), counter)
}

func TestContention(t *testing.T) {
	perthread := 1000
	threads := 100

	wg := sync.WaitGroup{}
	wg.Add(threads)

	g := &Group[int]{}

	for range threads {
		for range perthread {
			_, err := g.Do(context.TODO(), "foo", func(ctx context.Context) (int, error) {
				time.Sleep(time.Microsecond)
				return 0, nil
			})
			require.NoError(t, err)
		}
		wg.Done()
	}

	wg.Wait()
}

func TestMassiveParallel(t *testing.T) {
	var retryCount int64
	g := &Group[string]{}
	eg, ctx := errgroup.WithContext(context.Background())
	for range 1000 {
		eg.Go(func() error {
			_, err := g.Do(ctx, "key", func(ctx context.Context) (string, error) {
				return "", errors.Errorf("always fail")
			})
			if errors.Is(err, errRetryTimeout) {
				atomic.AddInt64(&retryCount, 1)
			}
			return err
		})
		// magic numbers to increase contention
		time.Sleep(5 * time.Microsecond)
	}
	err := eg.Wait()
	require.Error(t, err)
	assert.Equal(t, int64(0), retryCount)
}

func testFunc(wait time.Duration, ret string, counter *int64) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		atomic.AddInt64(counter, 1)
		select {
		case <-ctx.Done():
			return "", context.Cause(ctx)
		case <-time.After(wait):
			return ret, nil
		}
	}
}
