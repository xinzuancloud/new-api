package perfmetrics

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordRelaySamplePreservesPartialTokensWithoutReportingStreamSuccess(t *testing.T) {
	oldRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedisEnabled })
	for _, test := range []struct {
		name        string
		reason      relaycommon.StreamEndReason
		err         error
		softError   bool
		wantSuccess int64
	}{
		{name: "completed", reason: relaycommon.StreamEndReasonDone, wantSuccess: 1},
		{name: "legacy EOF", reason: relaycommon.StreamEndReasonEOF, wantSuccess: 1},
		{name: "deadline", reason: relaycommon.StreamEndReasonTimeout, err: context.DeadlineExceeded},
		{name: "client cancel", reason: relaycommon.StreamEndReasonClientGone, err: context.Canceled},
		{name: "read failure", reason: relaycommon.StreamEndReasonScannerErr, err: assert.AnError},
		{name: "handler failure", reason: relaycommon.StreamEndReasonHandlerStop, err: assert.AnError},
		{name: "soft error before done", reason: relaycommon.StreamEndReasonDone, softError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := relaycommon.NewStreamStatus()
			status.SetEndReason(test.reason, test.err)
			if test.softError {
				status.RecordError("invalid upstream chunk")
			}
			info := &relaycommon.RelayInfo{
				OriginModelName: t.Name(), UsingGroup: "test", IsStream: true,
				StartTime: time.Now().Add(-time.Second), StreamStatus: status,
			}
			RecordRelaySample(info, true, 7)
			var got counters
			var found bool
			hotBuckets.Range(func(key, value any) bool {
				if key.(bucketKey).model == info.OriginModelName {
					got = value.(*atomicBucket).snapshot()
					found = true
					hotBuckets.Delete(key)
				}
				return true
			})
			require.True(t, found)
			assert.EqualValues(t, 1, got.requestCount)
			assert.Equal(t, test.wantSuccess, got.successCount)
			assert.EqualValues(t, 7, got.outputTokens, "partial billed tokens remain measurable")
		})
	}
}
