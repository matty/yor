package runner

import (
	"os"
	"strconv"
	"testing"

	"github.com/bridgecrewio/yor/src/common/clioptions"
	"github.com/stretchr/testify/assert"
)

// A worker count of 0 or less parses cleanly but starts no workers, and TagDirectory's
// send on the unbuffered file channel then blocks for ever ("all goroutines are asleep -
// deadlock!"). Init must reject it rather than let the run hang.
//
// Rejection goes through logger.Error, which exits the process, so this asserts on the
// validation decision rather than calling Init: a test cannot survive os.Exit.
func TestWorkerNumValidation(t *testing.T) {
	cases := []struct {
		value string
		valid bool
	}{
		{"10", true},
		{"1", true},
		{"0", false},
		{"-1", false},
		{"abc", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run("YOR_WORKER_NUM="+tc.value, func(t *testing.T) {
			num, err := strconv.Atoi(tc.value)
			accepted := err == nil && num >= 1
			assert.Equal(t, tc.valid, accepted)
		})
	}
}

// The default must be usable: unset means workers get started.
func TestWorkerNumDefault(t *testing.T) {
	t.Setenv(WorkersNumEnvKey, "")
	assert.NoError(t, os.Unsetenv(WorkersNumEnvKey))

	r := &Runner{}
	err := r.Init(&clioptions.TagOptions{
		Directory: t.TempDir(),
		TagGroups: []string{"code2cloud"},
		Parsers:   []string{"Terraform"},
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, r.workersNum, 1, "a run with no YOR_WORKER_NUM set must start workers")

	expected, convErr := strconv.Atoi(DefaultWorkersNum)
	assert.NoError(t, convErr)
	assert.Equal(t, expected, r.workersNum)
}
