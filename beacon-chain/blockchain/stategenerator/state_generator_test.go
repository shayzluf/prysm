package stategenerator_test

import (
	"context"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/prysmaticlabs/prysm/beacon-chain/blockchain/stategenerator"
	"github.com/prysmaticlabs/prysm/beacon-chain/chaintest/backend"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	"github.com/prysmaticlabs/prysm/shared/featureconfig"
)

func init() {
	featureconfig.InitFeatureConfig(&featureconfig.FeatureFlagConfig{
		CacheTreeHash: false,
	})
}
func TestGenerateState_OK(t *testing.T) {
	t.Skip()
	b, err := backend.NewSimulatedBackend()
	if err != nil {
		t.Fatalf("Could not create a new simulated backend %v", err)
	}
	privKeys, err := b.SetupBackend(100)
	if err != nil {
		t.Fatalf("Could not set up backend %v", err)
	}
	beaconDb := b.DB()
	defer b.Shutdown()
	defer db.TeardownDB(beaconDb)
	ctx := context.Background()

	slotLimit := uint64(30)

	// Run the simulated chain for 30 slots, to get a state that we can save as finalized.
	for i := uint64(0); i < slotLimit; i++ {
		if err := b.GenerateBlockAndAdvanceChain(&backend.SimulatedObjects{}, privKeys); err != nil {
			t.Fatalf("Could not generate block and transition state successfully %v for slot %d", err, b.State().Slot+1)
		}
		inMemBlocks := b.InMemoryBlocks()
		if err := beaconDb.SaveBlock(inMemBlocks[len(inMemBlocks)-1]); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
		if err := beaconDb.UpdateChainHead(ctx, inMemBlocks[len(inMemBlocks)-1], b.State()); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
		if err := beaconDb.SaveFinalizedBlock(inMemBlocks[len(inMemBlocks)-1]); err != nil {
			t.Fatalf("Unable to save finalized state: %v", err)
		}
	}

	if err := beaconDb.SaveFinalizedState(b.State()); err != nil {
		t.Fatalf("Unable to save finalized state: %v", err)
	}

	// Run the chain for another 30 slots so that we can have this at the current head.
	for i := uint64(0); i < slotLimit; i++ {
		if err := b.GenerateBlockAndAdvanceChain(&backend.SimulatedObjects{}, privKeys); err != nil {
			t.Fatalf("Could not generate block and transition state successfully %v for slot %d", err, b.State().Slot+1)
		}
		inMemBlocks := b.InMemoryBlocks()
		if err := beaconDb.SaveBlock(inMemBlocks[len(inMemBlocks)-1]); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
		if err := beaconDb.UpdateChainHead(ctx, inMemBlocks[len(inMemBlocks)-1], b.State()); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
	}

	// Ran 30 slots to save finalized slot then ran another 30 slots.
	slotToGenerateTill := slotLimit * 2
	newState, err := stategenerator.GenerateStateFromBlock(context.Background(), beaconDb, slotToGenerateTill)
	if err != nil {
		t.Fatalf("Unable to generate new state from previous finalized state %v", err)
	}

	if newState.Slot != b.State().Slot {
		t.Fatalf("The generated state and the current state do not have the same slot, expected: %d but got %d",
			b.State().Slot, newState.Slot)
	}

	if !proto.Equal(newState, b.State()) {
		t.Error("Generated and saved states are unequal")
	}
}

func TestGenerateState_WithNilBlocksOK(t *testing.T) {
	t.Skip()
	b, err := backend.NewSimulatedBackend()
	if err != nil {
		t.Fatalf("Could not create a new simulated backend %v", err)
	}
	privKeys, err := b.SetupBackend(100)
	if err != nil {
		t.Fatalf("Could not set up backend %v", err)
	}
	beaconDb := b.DB()
	defer b.Shutdown()
	defer db.TeardownDB(beaconDb)
	ctx := context.Background()

	slotLimit := uint64(30)

	// Run the simulated chain for 30 slots, to get a state that we can save as finalized.
	for i := uint64(0); i < slotLimit; i++ {
		if err := b.GenerateBlockAndAdvanceChain(&backend.SimulatedObjects{}, privKeys); err != nil {
			t.Fatalf("Could not generate block and transition state successfully %v for slot %d", err, b.State().Slot+1)
		}
		inMemBlocks := b.InMemoryBlocks()
		if err := beaconDb.SaveBlock(inMemBlocks[len(inMemBlocks)-1]); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
		if err := beaconDb.UpdateChainHead(ctx, inMemBlocks[len(inMemBlocks)-1], b.State()); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
		if err := beaconDb.SaveFinalizedBlock(inMemBlocks[len(inMemBlocks)-1]); err != nil {
			t.Fatalf("Unable to save finalized state: %v", err)
		}
	}

	if err := beaconDb.SaveFinalizedState(b.State()); err != nil {
		t.Fatalf("Unable to save finalized state")
	}

	slotsWithNil := uint64(10)

	for i := uint64(0); i < slotLimit-slotsWithNil; i++ {
		if err := b.GenerateBlockAndAdvanceChain(&backend.SimulatedObjects{}, privKeys); err != nil {
			t.Fatalf("Could not generate block and transition state successfully %v for slot %d", err, b.State().Slot+1)
		}
		inMemBlocks := b.InMemoryBlocks()
		if err := beaconDb.SaveBlock(inMemBlocks[len(inMemBlocks)-1]); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
		if err := beaconDb.UpdateChainHead(ctx, inMemBlocks[len(inMemBlocks)-1], b.State()); err != nil {
			t.Fatalf("Unable to save block %v", err)
		}
	}

	// Ran 30 slots to save finalized slot then ran another 10 slots w/o blocks and 20 slots w/ blocks.
	slotToGenerateTill := slotLimit * 2
	newState, err := stategenerator.GenerateStateFromBlock(context.Background(), beaconDb, slotToGenerateTill)
	if err != nil {
		t.Fatalf("Unable to generate new state from previous finalized state %v", err)
	}

	if newState.Slot != b.State().Slot {
		t.Fatalf("The generated state and the current state do not have the same slot, expected: %d but got %d",
			b.State().Slot, newState.Slot)
	}

	if !proto.Equal(newState, b.State()) {
		t.Error("generated and saved states are unequal")
	}
}
