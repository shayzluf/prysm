package helpers

import (
	"fmt"
	"testing"

	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/params"
)

func TestIsActiveValidator_OK(t *testing.T) {
	tests := []struct {
		a uint64
		b bool
	}{
		{a: 0, b: false},
		{a: 10, b: true},
		{a: 100, b: false},
		{a: 1000, b: false},
		{a: 64, b: true},
	}
	for _, test := range tests {
		validator := &pb.Validator{ActivationEpoch: 10, ExitEpoch: 100}
		if IsActiveValidator(validator, test.a) != test.b {
			t.Errorf("IsActiveValidator(%d) = %v, want = %v",
				test.a, IsActiveValidator(validator, test.a), test.b)
		}
	}
}

func TestBeaconProposerIndex_OK(t *testing.T) {
	if params.BeaconConfig().SlotsPerEpoch != 64 {
		t.Errorf("SlotsPerEpoch should be 64 for these tests to pass")
	}

	validators := make([]*pb.Validator, params.BeaconConfig().DepositsForChainStart)
	for i := 0; i < len(validators); i++ {
		validators[i] = &pb.Validator{
			ExitEpoch: params.BeaconConfig().FarFutureEpoch,
		}
	}

	state := &pb.BeaconState{
		ValidatorRegistry:      validators,
		Slot:                   0,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}

	tests := []struct {
		slot  uint64
		index uint64
	}{
		{
			slot:  1,
			index: 8972,
		},
		{
			slot:  5,
			index: 1188,
		},
		{
			slot:  19,
			index: 2981,
		},
		{
			slot:  30,
			index: 1973,
		},
		{
			slot:  43,
			index: 11413,
		},
	}

	for _, tt := range tests {
		state.Slot = tt.slot
		result, err := BeaconProposerIndex(state)
		if err != nil {
			t.Errorf("Failed to get shard and committees at slot: %v", err)
		}

		if result != tt.index {
			t.Errorf(
				"Result index was an unexpected value. Wanted %d, got %d",
				tt.index,
				result,
			)
		}
	}
}

func TestBeaconProposerIndex_EmptyCommittee(t *testing.T) {
	beaconState := &pb.BeaconState{
		Slot:                   0,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	_, err := BeaconProposerIndex(beaconState)
	expected := fmt.Sprintf("empty first committee at slot %d", 0)
	if err.Error() != expected {
		t.Errorf("Unexpected error. got=%v want=%s", err, expected)
	}
}

func TestDelayedActivationExitEpoch_OK(t *testing.T) {
	epoch := uint64(9999)
	got := DelayedActivationExitEpoch(epoch)
	wanted := epoch + 1 + params.BeaconConfig().ActivationExitDelay
	if wanted != got {
		t.Errorf("Wanted: %d, received: %d", wanted, got)
	}
}

func TestChurnLimit_OK(t *testing.T) {
	tests := []struct {
		validatorCount int
		wantedChurn    uint64
	}{
		{validatorCount: 1000, wantedChurn: 4},
		{validatorCount: 100000, wantedChurn: 4},
		{validatorCount: 1000000, wantedChurn: 15 /* validatorCount/churnLimitQuotient */},
		{validatorCount: 2000000, wantedChurn: 30 /* validatorCount/churnLimitQuotient */},
	}
	for _, test := range tests {
		validators := make([]*pb.Validator, test.validatorCount)
		for i := 0; i < len(validators); i++ {
			validators[i] = &pb.Validator{
				ExitEpoch: params.BeaconConfig().FarFutureEpoch,
			}
		}

		beaconState := &pb.BeaconState{
			Slot:                   1,
			ValidatorRegistry:      validators,
			LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
			LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
		}
		resultChurn := ChurnLimit(beaconState)
		if resultChurn != test.wantedChurn {
			t.Errorf("ChurnLimit(%d) = %d, want = %d",
				test.validatorCount, resultChurn, test.wantedChurn)
		}
	}
}

func TestDomain_OK(t *testing.T) {
	state := &pb.BeaconState{
		Fork: &pb.Fork{
			Epoch:           3,
			PreviousVersion: []byte{0, 0, 0, 2},
			CurrentVersion:  []byte{0, 0, 0, 3},
		},
		Slot: 70,
	}

	if DomainVersion(state, 9, 1) != 4345298944 {
		t.Errorf("fork Version not equal to 4345298944 %d", DomainVersion(state, 1, 9))
	}

	if DomainVersion(state, 9, 2) != 8640266240 {
		t.Errorf("fork Version not equal to 8640266240 %d", DomainVersion(state, 2, 9))
	}

	if DomainVersion(state, 2, 1) != 4328521728 {
		t.Errorf("fork Version not equal to 4328521728 %d", DomainVersion(state, 1, 2))
	}
	if DomainVersion(state, 2, 2) != 8623489024 {
		t.Errorf("fork Version not equal to 8623489024 %d", DomainVersion(state, 2, 2))
	}
	if DomainVersion(state, 0, 1) != 4328521728 {
		t.Errorf("fork Version not equal to 4328521728 %d", DomainVersion(state, 1, 0))
	}
}
