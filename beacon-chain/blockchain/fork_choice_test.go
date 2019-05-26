package blockchain

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/prysmaticlabs/prysm/beacon-chain/attestation"
	"github.com/prysmaticlabs/prysm/beacon-chain/cache"
	b "github.com/prysmaticlabs/prysm/beacon-chain/core/blocks"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	"github.com/prysmaticlabs/prysm/beacon-chain/internal"
	"github.com/prysmaticlabs/prysm/beacon-chain/powchain"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/bls"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

// Ensure ChainService implements interfaces.
var _ = ForkChoice(&ChainService{})
var endpoint = "ws://127.0.0.1"

func TestApplyForkChoice_SetsCanonicalHead(t *testing.T) {
	deposits, _ := setupInitialDeposits(t, 5)
	beaconState, err := state.GenesisBeaconState(deposits, 0, nil)
	if err != nil {
		t.Fatalf("Cannot create genesis beacon state: %v", err)
	}
	stateRoot, err := hashutil.HashProto(beaconState)
	if err != nil {
		t.Fatalf("Could not tree hash state: %v", err)
	}
	genesis := b.NewGenesisBlock(stateRoot[:])
	genesisRoot, err := hashutil.HashProto(genesis)
	if err != nil {
		t.Fatalf("Could not get genesis block root: %v", err)
	}

	// Table driven tests for various fork choice scenarios.
	tests := []struct {
		blockSlot uint64
		state     *pb.BeaconState
		logAssert string
	}{
		// Higher slot but same state should trigger chain update.
		{
			blockSlot: 64,
			state:     beaconState,
			logAssert: "Chain head block and state updated",
		},
		// Higher slot, different state, but higher last finalized slot.
		{
			blockSlot: 64,
			state:     &pb.BeaconState{FinalizedEpoch: 2},
			logAssert: "Chain head block and state updated",
		},
		// Higher slot, different state, same last finalized slot,
		// but last justified slot.
		{
			blockSlot: 64,
			state: &pb.BeaconState{
				FinalizedEpoch:        0,
				CurrentJustifiedEpoch: 2,
			},
			logAssert: "Chain head block and state updated",
		},
	}
	for _, tt := range tests {
		hook := logTest.NewGlobal()
		beaconDb := internal.SetupDB(t)
		defer internal.TeardownDB(t, beaconDb)
		attsService := attestation.NewAttestationService(
			context.Background(),
			&attestation.Config{BeaconDB: beaconDb})

		chainService := setupBeaconChain(t, beaconDb, attsService)
		if err := chainService.beaconDB.SaveBlock(
			genesis); err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveJustifiedBlock(
			genesis); err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveJustifiedState(
			beaconState); err != nil {
			t.Fatal(err)
		}
		unixTime := uint64(time.Now().Unix())
		deposits, _ := setupInitialDeposits(t, 100)
		if err := beaconDb.InitializeState(context.Background(), unixTime, deposits, &pb.Eth1Data{}); err != nil {
			t.Fatalf("Could not initialize beacon state to disk: %v", err)
		}

		stateRoot, err := hashutil.HashProto(tt.state)
		if err != nil {
			t.Fatalf("Could not tree hash state: %v", err)
		}
		block := &pb.BeaconBlock{
			Slot:            tt.blockSlot,
			StateRoot:       stateRoot[:],
			ParentBlockRoot: genesisRoot[:],
			Eth1Data: &pb.Eth1Data{
				DepositRoot: []byte("a"),
				BlockRoot:   []byte("b"),
			},
		}
		blockRoot, err := hashutil.HashBeaconBlock(block)
		if err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveBlock(block); err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveHistoricalState(context.Background(), beaconState, blockRoot); err != nil {
			t.Fatal(err)
		}
		if err := chainService.ApplyForkChoiceRule(context.Background(), block, tt.state); err != nil {
			t.Errorf("Expected head to update, received %v", err)
		}
		chainService.cancel()
		testutil.AssertLogsContain(t, hook, tt.logAssert)
	}
}

func TestVoteCount_ParentDoesNotExistNoVoteCount(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	genesisBlock := b.NewGenesisBlock([]byte("stateroot"))
	if err := beaconDB.SaveBlock(genesisBlock); err != nil {
		t.Fatal(err)
	}
	potentialHead := &pb.BeaconBlock{
		ParentBlockRoot: []byte{'A'}, // We give a bogus parent root hash.
	}
	if err := beaconDB.SaveBlock(potentialHead); err != nil {
		t.Fatal(err)
	}
	headRoot, err := hashutil.HashBeaconBlock(potentialHead)
	if err != nil {
		t.Fatal(err)
	}

	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       potentialHead.Slot,
		BlockRoot:  headRoot[:],
		ParentRoot: potentialHead.ParentBlockRoot,
	}
	count, err := VoteCount(genesisBlock, &pb.BeaconState{}, voteTargets, beaconDB)
	if err != nil {
		t.Fatalf("Could not get vote count: %v", err)
	}
	if count != 0 {
		t.Errorf("Wanted vote count 0, got: %d", count)
	}
}

func TestVoteCount_IncreaseCountCorrectly(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	genesisBlock := b.NewGenesisBlock([]byte("stateroot"))
	genesisRoot, err := hashutil.HashBeaconBlock(genesisBlock)
	if err != nil {
		t.Fatal(err)
	}
	if err := beaconDB.SaveBlock(genesisBlock); err != nil {
		t.Fatal(err)
	}

	potentialHead := &pb.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: genesisRoot[:],
	}
	headRoot1, err := hashutil.HashBeaconBlock(potentialHead)
	if err != nil {
		t.Fatal(err)
	}

	potentialHead2 := &pb.BeaconBlock{
		Slot:            6,
		ParentBlockRoot: genesisRoot[:],
	}
	headRoot2, err := hashutil.HashBeaconBlock(potentialHead2)
	if err != nil {
		t.Fatal(err)
	}
	// We store these potential heads in the DB.
	if err := beaconDB.SaveBlock(potentialHead); err != nil {
		t.Fatal(err)
	}
	if err := beaconDB.SaveBlock(potentialHead2); err != nil {
		t.Fatal(err)
	}
	beaconState := &pb.BeaconState{ValidatorRegistry: []*pb.Validator{{EffectiveBalance: 1e9}, {EffectiveBalance: 1e9}}}
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       potentialHead.Slot,
		BlockRoot:  headRoot1[:],
		ParentRoot: potentialHead.ParentBlockRoot,
	}
	voteTargets[1] = &pb.AttestationTarget{
		Slot:       potentialHead2.Slot,
		BlockRoot:  headRoot2[:],
		ParentRoot: potentialHead2.ParentBlockRoot,
	}
	count, err := VoteCount(genesisBlock, beaconState, voteTargets, beaconDB)
	if err != nil {
		t.Fatalf("Could not fetch vote balances: %v", err)
	}
	if count != 2e9 {
		t.Errorf("Expected total balances 2e9, received %d", count)
	}
}

func TestAttestationTargets_RetrieveWorks(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	pubKey := []byte{'A'}
	beaconState := &pb.BeaconState{
		ValidatorRegistry: []*pb.Validator{{
			Pubkey:    pubKey,
			ExitEpoch: params.BeaconConfig().FarFutureEpoch}},
	}

	if err := beaconDB.SaveState(ctx, beaconState); err != nil {
		t.Fatalf("could not save state: %v", err)
	}

	block := &pb.BeaconBlock{Slot: 100}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatalf("could not save block: %v", err)
	}
	blockRoot, err := hashutil.HashBeaconBlock(block)
	if err != nil {
		t.Fatalf("could not hash block: %v", err)
	}
	if err := beaconDB.SaveAttestationTarget(ctx, &pb.AttestationTarget{
		Slot:       block.Slot,
		BlockRoot:  blockRoot[:],
		ParentRoot: []byte{},
	}); err != nil {
		t.Fatalf("could not save att tgt: %v", err)
	}

	attsService := attestation.NewAttestationService(
		context.Background(),
		&attestation.Config{BeaconDB: beaconDB})

	att := &pb.Attestation{
		Data: &pb.AttestationData{
			BeaconBlockRootHash32: blockRoot[:],
		}}
	pubKey48 := bytesutil.ToBytes48(pubKey)
	attsService.InsertAttestationIntoStore(pubKey48, att)

	chainService := setupBeaconChain(t, beaconDB, attsService)
	attestationTargets, err := chainService.AttestationTargets(beaconState)
	if err != nil {
		t.Fatalf("Could not get attestation targets: %v", err)
	}
	if attestationTargets[0].Slot != block.Slot {
		t.Errorf("Wanted attested slot %d, got %d", block.Slot, attestationTargets[0].Slot)
	}
}

func TestBlockChildren_2InARow(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	chainService := setupBeaconChain(t, beaconDB, nil)

	beaconState := &pb.BeaconState{
		Slot: 3,
	}

	// Construct the following chain:
	// B1 <- B2 <- B3  (State is slot 3)
	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block1, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block2 := &pb.BeaconBlock{
		Slot:            2,
		ParentBlockRoot: root1[:],
	}
	root2, err := hashutil.HashBeaconBlock(block2)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block2); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block2, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block3 := &pb.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: root2[:],
	}
	if err = chainService.beaconDB.SaveBlock(block3); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block3, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	childrenBlock, err := chainService.BlockChildren(ctx, block1, beaconState.Slot)
	if err != nil {
		t.Fatalf("Could not get block children: %v", err)
	}

	// When we input block B1, we should get B2 back.
	wanted := []*pb.BeaconBlock{block2}
	if !reflect.DeepEqual(wanted, childrenBlock) {
		t.Errorf("Wrong children block received, want %v, received %v", wanted, childrenBlock)
	}
}

func TestBlockChildren_ChainSplits(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	chainService := setupBeaconChain(t, beaconDB, nil)

	beaconState := &pb.BeaconState{
		Slot: 10,
	}

	// Construct the following chain:
	//     /- B2
	// B1 <- B3 (State is slot 10)
	//      \- B4
	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block1, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block2 := &pb.BeaconBlock{
		Slot:            2,
		ParentBlockRoot: root1[:],
	}
	if err = chainService.beaconDB.SaveBlock(block2); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block2, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block3 := &pb.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: root1[:],
	}
	if err = chainService.beaconDB.SaveBlock(block3); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block3, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block4 := &pb.BeaconBlock{
		Slot:            4,
		ParentBlockRoot: root1[:],
	}
	if err = chainService.beaconDB.SaveBlock(block4); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block4, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	childrenBlock, err := chainService.BlockChildren(ctx, block1, beaconState.Slot)
	if err != nil {
		t.Fatalf("Could not get block children: %v", err)
	}

	// When we input block B1, we should get B2, B3 and B4 back.
	wanted := []*pb.BeaconBlock{block2, block3, block4}
	if !reflect.DeepEqual(wanted, childrenBlock) {
		t.Errorf("Wrong children block received")
	}
}

func TestBlockChildren_SkipSlots(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	chainService := setupBeaconChain(t, beaconDB, nil)

	beaconState := &pb.BeaconState{
		Slot: 10,
	}

	// Construct the following chain:
	// B1 <- B5 <- B9 (State is slot 10)
	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block1, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block5 := &pb.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: root1[:],
	}
	root2, err := hashutil.HashBeaconBlock(block5)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block5); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block5, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block9 := &pb.BeaconBlock{
		Slot:            9,
		ParentBlockRoot: root2[:],
	}
	if err = chainService.beaconDB.SaveBlock(block9); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block9, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	childrenBlock, err := chainService.BlockChildren(ctx, block1, beaconState.Slot)
	if err != nil {
		t.Fatalf("Could not get block children: %v", err)
	}

	// When we input block B1, we should get B5.
	wanted := []*pb.BeaconBlock{block5}
	if !reflect.DeepEqual(wanted, childrenBlock) {
		t.Errorf("Wrong children block received")
	}
}

func TestLMDGhost_TrivialHeadUpdate(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	beaconState := &pb.BeaconState{
		Slot:              10,
		Balances:          []uint64{params.BeaconConfig().MaxDepositAmount},
		ValidatorRegistry: []*pb.Validator{{}},
	}

	chainService := setupBeaconChain(t, beaconDB, nil)

	// Construct the following chain:
	// B1 - B2 (State is slot 2)
	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block1, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block2 := &pb.BeaconBlock{
		Slot:            2,
		ParentBlockRoot: root1[:],
	}
	block2Root, err := hashutil.HashBeaconBlock(block2)
	if err != nil {
		t.Fatal(err)
	}
	if err = chainService.beaconDB.SaveBlock(block2); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	beaconState.LatestBlock = block2
	if err = chainService.beaconDB.UpdateChainHead(ctx, block2, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	// The only vote is on block 2.
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       block2.Slot,
		BlockRoot:  block2Root[:],
		ParentRoot: block2.ParentBlockRoot,
	}

	// LMDGhost should pick block 2.
	head, err := chainService.lmdGhost(ctx, block1, beaconState, voteTargets)
	if err != nil {
		t.Fatalf("Could not run LMD GHOST: %v", err)
	}
	if !reflect.DeepEqual(block2, head) {
		t.Errorf("Expected head to equal %v, received %v", block2, head)
	}
}

func TestLMDGhost_3WayChainSplitsSameHeight(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	beaconState := &pb.BeaconState{
		Slot: 10,
		Balances: []uint64{
			params.BeaconConfig().MaxDepositAmount,
			params.BeaconConfig().MaxDepositAmount,
			params.BeaconConfig().MaxDepositAmount,
			params.BeaconConfig().MaxDepositAmount},
		ValidatorRegistry: []*pb.Validator{{EffectiveBalance: params.BeaconConfig().MaxDepositAmount},
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount},
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount},
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount}},
	}

	chainService := setupBeaconChain(t, beaconDB, nil)

	// Construct the following chain:
	//    /- B2
	// B1  - B3 (State is slot 10)
	//    \- B4
	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block1, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block2 := &pb.BeaconBlock{
		Slot:            2,
		ParentBlockRoot: root1[:],
	}
	root2, err := hashutil.HashBeaconBlock(block2)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block2); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block2, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block3 := &pb.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: root1[:],
	}
	root3, err := hashutil.HashBeaconBlock(block3)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block3); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block3, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block4 := &pb.BeaconBlock{
		Slot:            4,
		ParentBlockRoot: root1[:],
	}
	root4, err := hashutil.HashBeaconBlock(block4)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block4); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block4, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	// Give block 4 the most votes (2).
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       block2.Slot,
		BlockRoot:  root2[:],
		ParentRoot: block2.ParentBlockRoot,
	}
	voteTargets[1] = &pb.AttestationTarget{
		Slot:       block3.Slot,
		BlockRoot:  root3[:],
		ParentRoot: block3.ParentBlockRoot,
	}
	voteTargets[2] = &pb.AttestationTarget{
		Slot:       block4.Slot,
		BlockRoot:  root4[:],
		ParentRoot: block4.ParentBlockRoot,
	}
	voteTargets[3] = &pb.AttestationTarget{
		Slot:       block4.Slot,
		BlockRoot:  root4[:],
		ParentRoot: block4.ParentBlockRoot,
	}
	// LMDGhost should pick block 4.
	head, err := chainService.lmdGhost(ctx, block1, beaconState, voteTargets)
	if err != nil {
		t.Fatalf("Could not run LMD GHOST: %v", err)
	}
	if !reflect.DeepEqual(block4, head) {
		t.Errorf("Expected head to equal %v, received %v", block4, head)
	}
}

func TestIsDescendant_Ok(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	chainService := setupBeaconChain(t, beaconDB, nil)

	// Construct the following chain:
	// B1  - B2 - B3
	//    \- B4 - B5
	// Prove the following:
	// 	B5 is not a descendant of B2
	// 	B3 is not a descendant of B4
	//  B5 and B3 are descendants of B1

	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	block2 := &pb.BeaconBlock{
		Slot:            2,
		ParentBlockRoot: root1[:],
	}
	root2, err := hashutil.HashBeaconBlock(block2)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block2); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	block3 := &pb.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: root2[:],
	}
	_, err = hashutil.HashBeaconBlock(block3)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block3); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	block4 := &pb.BeaconBlock{
		Slot:            4,
		ParentBlockRoot: root1[:],
	}
	root4, err := hashutil.HashBeaconBlock(block4)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block4); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	block5 := &pb.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: root4[:],
	}
	_, err = hashutil.HashBeaconBlock(block5)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block5); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}

	isDescendant, err := chainService.isDescendant(block2, block5)
	if err != nil {
		t.Fatal(err)
	}
	if isDescendant {
		t.Errorf("block%d can't be descendant of block%d", block5.Slot, block2.Slot)
	}
	isDescendant, _ = chainService.isDescendant(block4, block3)
	if isDescendant {
		t.Errorf("block%d can't be descendant of block%d", block3.Slot, block4.Slot)
	}
	isDescendant, _ = chainService.isDescendant(block1, block5)
	if !isDescendant {
		t.Errorf("block%d is the descendant of block%d", block3.Slot, block1.Slot)
	}
	isDescendant, _ = chainService.isDescendant(block1, block3)
	if !isDescendant {
		t.Errorf("block%d is the descendant of block%d", block3.Slot, block1.Slot)
	}
}

func TestLMDGhost_2WayChainSplitsDiffHeight(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	beaconState := &pb.BeaconState{
		Slot: 10,
		ValidatorRegistry: []*pb.Validator{
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount},
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount},
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount},
			{EffectiveBalance: params.BeaconConfig().MaxDepositAmount}},
	}

	chainService := setupBeaconChain(t, beaconDB, nil)

	// Construct the following chain:
	//    /- B2 - B4 - B6
	// B1  - B3 - B5 (State is slot 10)
	block1 := &pb.BeaconBlock{
		Slot:            1,
		ParentBlockRoot: []byte{'A'},
	}
	root1, err := hashutil.HashBeaconBlock(block1)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block1); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block1, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block2 := &pb.BeaconBlock{
		Slot:            2,
		ParentBlockRoot: root1[:],
	}
	root2, err := hashutil.HashBeaconBlock(block2)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block2); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block2, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block3 := &pb.BeaconBlock{
		Slot:            3,
		ParentBlockRoot: root1[:],
	}
	root3, err := hashutil.HashBeaconBlock(block3)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block3); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block3, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block4 := &pb.BeaconBlock{
		Slot:            4,
		ParentBlockRoot: root2[:],
	}
	root4, err := hashutil.HashBeaconBlock(block4)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block4); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block4, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block5 := &pb.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: root3[:],
	}
	root5, err := hashutil.HashBeaconBlock(block5)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block5); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block5, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	block6 := &pb.BeaconBlock{
		Slot:            6,
		ParentBlockRoot: root4[:],
	}
	root6, err := hashutil.HashBeaconBlock(block6)
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(block6); err != nil {
		t.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, block6, beaconState); err != nil {
		t.Fatalf("Could update chain head: %v", err)
	}

	// Give block 5 the most votes (2).
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       block6.Slot,
		BlockRoot:  root6[:],
		ParentRoot: block6.ParentBlockRoot,
	}
	voteTargets[1] = &pb.AttestationTarget{
		Slot:       block5.Slot,
		BlockRoot:  root5[:],
		ParentRoot: block5.ParentBlockRoot,
	}
	voteTargets[2] = &pb.AttestationTarget{
		Slot:       block5.Slot,
		BlockRoot:  root5[:],
		ParentRoot: block5.ParentBlockRoot,
	}
	// LMDGhost should pick block 5.
	head, err := chainService.lmdGhost(ctx, block1, beaconState, voteTargets)
	if err != nil {
		t.Fatalf("Could not run LMD GHOST: %v", err)
	}
	if !reflect.DeepEqual(block5, head) {
		t.Errorf("Expected head to equal %v, received %v", block5, head)
	}
}

// This benchmarks LMD GHOST fork choice using 8 blocks in a row.
// 8 validators and all validators voted on the last block.
// Ex:
// 	B0 - B1 - B2 - B3 - B4 - B5 - B6 - B7 (8 votes)
func BenchmarkLMDGhost_8Slots_8Validators(b *testing.B) {
	beaconDB := internal.SetupDB(b)
	defer internal.TeardownDB(b, beaconDB)
	ctx := context.Background()

	validatorCount := 8
	balances := make([]uint64, validatorCount)
	for i := 0; i < validatorCount; i++ {
		balances[i] = params.BeaconConfig().MaxDepositAmount
	}

	chainService := setupBeaconChainBenchmark(b, beaconDB)

	// Construct 8 blocks. (Epoch length = 8)
	epochLength := uint64(8)
	beaconState := &pb.BeaconState{
		Slot:     epochLength,
		Balances: balances,
	}
	genesis := &pb.BeaconBlock{
		Slot:            0,
		ParentBlockRoot: []byte{},
	}
	root, err := hashutil.HashBeaconBlock(genesis)
	if err != nil {
		b.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(genesis); err != nil {
		b.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, genesis, beaconState); err != nil {
		b.Fatalf("Could update chain head: %v", err)
	}

	var block *pb.BeaconBlock
	for i := 1; i < int(epochLength); i++ {
		block = &pb.BeaconBlock{
			Slot:            uint64(i),
			ParentBlockRoot: root[:],
		}
		if err = chainService.beaconDB.SaveBlock(block); err != nil {
			b.Fatalf("Could not save block: %v", err)
		}
		if err = chainService.beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
			b.Fatalf("Could update chain head: %v", err)
		}
		root, err = hashutil.HashBeaconBlock(block)
		if err != nil {
			b.Fatalf("Could not hash block: %v", err)
		}
	}

	blockRoot, err := hashutil.HashBeaconBlock(block)
	if err != nil {
		b.Fatal(err)
	}

	voteTargets := make(map[uint64]*pb.AttestationTarget)
	target := &pb.AttestationTarget{
		Slot:       block.Slot,
		BlockRoot:  blockRoot[:],
		ParentRoot: block.ParentBlockRoot,
	}
	for i := 0; i < validatorCount; i++ {
		voteTargets[uint64(i)] = target
	}

	for i := 0; i < b.N; i++ {
		_, err := chainService.lmdGhost(ctx, genesis, beaconState, voteTargets)
		if err != nil {
			b.Fatalf("Could not run LMD GHOST: %v", err)
		}
	}
}

// This benchmarks LMD GHOST fork choice 32 blocks in a row.
// This is assuming the worst case where no finalization happens
// for 4 epochs in our Sapphire test net. (epoch length is 8 slots)
// 8 validators and all validators voted on the last block.
// Ex:
// 	B0 - B1 - B2 - B3 - B4 - B5 - B6 - B7 (8 votes)
func BenchmarkLMDGhost_32Slots_8Validators(b *testing.B) {
	beaconDB := internal.SetupDB(b)
	defer internal.TeardownDB(b, beaconDB)
	ctx := context.Background()

	validatorCount := 8
	balances := make([]uint64, validatorCount)
	for i := 0; i < validatorCount; i++ {
		balances[i] = params.BeaconConfig().MaxDepositAmount
	}

	chainService := setupBeaconChainBenchmark(b, beaconDB)

	// Construct 8 blocks. (Epoch length = 8)
	epochLength := uint64(8)
	beaconState := &pb.BeaconState{
		Slot:     epochLength,
		Balances: balances,
	}
	genesis := &pb.BeaconBlock{
		Slot:            0,
		ParentBlockRoot: []byte{},
	}
	root, err := hashutil.HashBeaconBlock(genesis)
	if err != nil {
		b.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(genesis); err != nil {
		b.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, genesis, beaconState); err != nil {
		b.Fatalf("Could update chain head: %v", err)
	}

	var block *pb.BeaconBlock
	for i := 1; i < int(epochLength); i++ {
		block = &pb.BeaconBlock{
			Slot:            uint64(i),
			ParentBlockRoot: root[:],
		}
		if err = chainService.beaconDB.SaveBlock(block); err != nil {
			b.Fatalf("Could not save block: %v", err)
		}
		if err = chainService.beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
			b.Fatalf("Could update chain head: %v", err)
		}
		root, err = hashutil.HashBeaconBlock(block)
		if err != nil {
			b.Fatalf("Could not hash block: %v", err)
		}
	}

	blockRoot, err := hashutil.HashBeaconBlock(block)
	if err != nil {
		b.Fatal(err)
	}

	voteTargets := make(map[uint64]*pb.AttestationTarget)
	target := &pb.AttestationTarget{
		Slot:       block.Slot,
		BlockRoot:  blockRoot[:],
		ParentRoot: block.ParentBlockRoot,
	}
	for i := 0; i < validatorCount; i++ {
		voteTargets[uint64(i)] = target
	}

	for i := 0; i < b.N; i++ {
		_, err := chainService.lmdGhost(ctx, genesis, beaconState, voteTargets)
		if err != nil {
			b.Fatalf("Could not run LMD GHOST: %v", err)
		}
	}
}

// This test benchmarks LMD GHOST fork choice using 32 blocks in a row.
// 64 validators and all validators voted on the last block.
// Ex:
// 	B0 - B1 - B2 - ... - B32 (64 votes)
func BenchmarkLMDGhost_32Slots_64Validators(b *testing.B) {
	beaconDB := internal.SetupDB(b)
	defer internal.TeardownDB(b, beaconDB)
	ctx := context.Background()

	validatorCount := 64
	balances := make([]uint64, validatorCount)
	for i := 0; i < validatorCount; i++ {
		balances[i] = params.BeaconConfig().MaxDepositAmount
	}

	chainService := setupBeaconChainBenchmark(b, beaconDB)

	// Construct 64 blocks. (Epoch length = 64)
	epochLength := uint64(32)
	beaconState := &pb.BeaconState{
		Slot:     epochLength,
		Balances: balances,
	}
	genesis := &pb.BeaconBlock{
		Slot:            0,
		ParentBlockRoot: []byte{},
	}
	root, err := hashutil.HashBeaconBlock(genesis)
	if err != nil {
		b.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(genesis); err != nil {
		b.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, genesis, beaconState); err != nil {
		b.Fatalf("Could update chain head: %v", err)
	}

	var block *pb.BeaconBlock
	for i := 1; i < int(epochLength); i++ {
		block = &pb.BeaconBlock{
			Slot:            uint64(i),
			ParentBlockRoot: root[:],
		}
		if err = chainService.beaconDB.SaveBlock(block); err != nil {
			b.Fatalf("Could not save block: %v", err)
		}
		if err = chainService.beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
			b.Fatalf("Could update chain head: %v", err)
		}
		root, err = hashutil.HashBeaconBlock(block)
		if err != nil {
			b.Fatalf("Could not hash block: %v", err)
		}
	}

	blockRoot, err := hashutil.HashBeaconBlock(block)
	if err != nil {
		b.Fatal(err)
	}

	voteTargets := make(map[uint64]*pb.AttestationTarget)
	target := &pb.AttestationTarget{
		Slot:       block.Slot,
		BlockRoot:  blockRoot[:],
		ParentRoot: block.ParentBlockRoot,
	}
	for i := 0; i < validatorCount; i++ {
		voteTargets[uint64(i)] = target
	}

	for i := 0; i < b.N; i++ {
		_, err := chainService.lmdGhost(ctx, genesis, beaconState, voteTargets)
		if err != nil {
			b.Fatalf("Could not run LMD GHOST: %v", err)
		}
	}
}

// This test benchmarks LMD GHOST fork choice using 64 blocks in a row.
// 16384 validators and all validators voted on the last block.
// Ex:
// 	B0 - B1 - B2 - ... - B64 (16384 votes)
func BenchmarkLMDGhost_64Slots_16384Validators(b *testing.B) {
	beaconDB := internal.SetupDB(b)
	defer internal.TeardownDB(b, beaconDB)
	ctx := context.Background()

	validatorCount := 16384
	balances := make([]uint64, validatorCount)
	for i := 0; i < validatorCount; i++ {
		balances[i] = params.BeaconConfig().MaxDepositAmount
	}

	chainService := setupBeaconChainBenchmark(b, beaconDB)

	// Construct 64 blocks. (Epoch length = 64)
	epochLength := uint64(64)
	beaconState := &pb.BeaconState{
		Slot:     epochLength,
		Balances: balances,
	}
	genesis := &pb.BeaconBlock{
		Slot:            0,
		ParentBlockRoot: []byte{},
	}
	root, err := hashutil.HashBeaconBlock(genesis)
	if err != nil {
		b.Fatalf("Could not hash block: %v", err)
	}
	if err = chainService.beaconDB.SaveBlock(genesis); err != nil {
		b.Fatalf("Could not save block: %v", err)
	}
	if err = chainService.beaconDB.UpdateChainHead(ctx, genesis, beaconState); err != nil {
		b.Fatalf("Could update chain head: %v", err)
	}

	var block *pb.BeaconBlock
	for i := 1; i < int(epochLength); i++ {
		block = &pb.BeaconBlock{
			Slot:            uint64(i),
			ParentBlockRoot: root[:],
		}
		if err = chainService.beaconDB.SaveBlock(block); err != nil {
			b.Fatalf("Could not save block: %v", err)
		}
		if err = chainService.beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
			b.Fatalf("Could update chain head: %v", err)
		}
		root, err = hashutil.HashBeaconBlock(block)
		if err != nil {
			b.Fatalf("Could not hash block: %v", err)
		}
	}

	blockRoot, err := hashutil.HashBeaconBlock(block)
	if err != nil {
		b.Fatal(err)
	}

	voteTargets := make(map[uint64]*pb.AttestationTarget)
	target := &pb.AttestationTarget{
		Slot:       block.Slot,
		BlockRoot:  blockRoot[:],
		ParentRoot: block.ParentBlockRoot,
	}
	for i := 0; i < validatorCount; i++ {
		voteTargets[uint64(i)] = target
	}

	for i := 0; i < b.N; i++ {
		_, err := chainService.lmdGhost(ctx, genesis, beaconState, voteTargets)
		if err != nil {
			b.Fatalf("Could not run LMD GHOST: %v", err)
		}
	}
}

func setupBeaconChainBenchmark(b *testing.B, beaconDB *db.BeaconDB) *ChainService {
	ctx := context.Background()
	var web3Service *powchain.Web3Service
	var err error
	client := &faultyClient{}
	web3Service, err = powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{
		Endpoint:        endpoint,
		DepositContract: common.Address{},
		Reader:          client,
		Client:          client,
		Logger:          client,
	})

	if err != nil {
		b.Fatalf("unable to set up web3 service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       beaconDB,
		Web3Service:    web3Service,
		OpsPoolService: &mockOperationService{},
		AttsService:    nil,
	}
	if err != nil {
		b.Fatalf("could not register blockchain service: %v", err)
	}
	chainService, err := NewChainService(ctx, cfg)
	if err != nil {
		b.Fatalf("unable to setup chain service: %v", err)
	}

	return chainService
}

func TestUpdateFFGCheckPts_NewJustifiedSlot(t *testing.T) {
	genesisSlot := uint64(0)
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	chainSvc := setupBeaconChain(t, beaconDB, nil)
	gBlockRoot, gBlock, gState, privKeys := setupFFGTest(t)
	if err := chainSvc.beaconDB.SaveBlock(gBlock); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.UpdateChainHead(ctx, gBlock, gState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.SaveFinalizedState(gState); err != nil {
		t.Fatal(err)
	}

	// Last justified check point happened at slot 0.
	if err := chainSvc.beaconDB.SaveJustifiedBlock(
		&pb.BeaconBlock{Slot: genesisSlot}); err != nil {
		t.Fatal(err)
	}

	// Also saved finalized block to slot 0 to test justification case only.
	if err := chainSvc.beaconDB.SaveFinalizedBlock(&pb.BeaconBlock{Slot: genesisSlot}); err != nil {
		t.Fatal(err)
	}

	// New justified slot in state is at slot 64.
	offset := uint64(64)
	proposerIdx, err := helpers.BeaconProposerIndex(gState)
	if err != nil {
		t.Fatal(err)
	}
	gState.CurrentJustifiedEpoch = 1
	gState.Slot = genesisSlot + offset
	buf := make([]byte, 32)
	binary.LittleEndian.PutUint64(buf, gState.CurrentJustifiedEpoch)
	domain := helpers.DomainVersion(gState, gState.CurrentJustifiedEpoch, params.BeaconConfig().DomainRandao)
	epochSignature := privKeys[proposerIdx].Sign(buf, domain)
	block := &pb.BeaconBlock{
		Slot:            genesisSlot + offset,
		ParentBlockRoot: gBlockRoot[:],
		Body: &pb.BeaconBlockBody{
			RandaoReveal: epochSignature.Marshal(),
		}}
	if err := chainSvc.beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.UpdateChainHead(ctx, block, gState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.updateFFGCheckPts(ctx, gState); err != nil {
		t.Fatal(err)
	}

	// Getting latest justification check point from DB and
	// verify they have been updated.
	newJustifiedState, err := chainSvc.beaconDB.JustifiedState()
	if err != nil {
		t.Fatal(err)
	}
	newJustifiedBlock, err := chainSvc.beaconDB.JustifiedBlock()
	if err != nil {
		t.Fatal(err)
	}
	if newJustifiedState.Slot-genesisSlot != offset {
		t.Errorf("Wanted justification state slot: %d, got: %d",
			offset, newJustifiedState.Slot-genesisSlot)
	}
	if newJustifiedBlock.Slot-genesisSlot != offset {
		t.Errorf("Wanted justification block slot: %d, got: %d",
			offset, newJustifiedBlock.Slot-genesisSlot)
	}
}

func TestUpdateFFGCheckPts_NewFinalizedSlot(t *testing.T) {
	genesisSlot := uint64(0)
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	chainSvc := setupBeaconChain(t, beaconDB, nil)
	ctx := context.Background()

	gBlockRoot, gBlock, gState, privKeys := setupFFGTest(t)
	if err := chainSvc.beaconDB.SaveBlock(gBlock); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.UpdateChainHead(ctx, gBlock, gState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.SaveFinalizedState(gState); err != nil {
		t.Fatal(err)
	}

	// Last finalized check point happened at slot 0.
	if err := chainSvc.beaconDB.SaveFinalizedBlock(
		gBlock); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.SaveFinalizedState(
		gState); err != nil {
		t.Fatal(err)
	}

	// New Finalized slot in state is at slot 64.
	offset := uint64(64)
	proposerIdx, err := helpers.BeaconProposerIndex(gState)
	if err != nil {
		t.Fatal(err)
	}

	// Also saved justified block to slot 0 to test finalized case only.
	if err := chainSvc.beaconDB.SaveJustifiedBlock(
		&pb.BeaconBlock{Slot: genesisSlot}); err != nil {
		t.Fatal(err)
	}

	gState.FinalizedEpoch = 1
	gState.Slot = genesisSlot + offset
	buf := make([]byte, 32)
	binary.LittleEndian.PutUint64(buf, gState.FinalizedEpoch)
	domain := helpers.DomainVersion(gState, gState.FinalizedEpoch, params.BeaconConfig().DomainRandao)
	epochSignature := privKeys[proposerIdx].Sign(buf, domain)
	block := &pb.BeaconBlock{
		Slot:            genesisSlot + offset,
		ParentBlockRoot: gBlockRoot[:],
		Body: &pb.BeaconBlockBody{
			RandaoReveal: epochSignature.Marshal(),
		}}

	if err := chainSvc.beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.UpdateChainHead(ctx, block, gState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.updateFFGCheckPts(ctx, gState); err != nil {
		t.Fatal(err)
	}

	// Getting latest justification check point from DB and
	// verify they have been updated.
	newFinalizedState, err := chainSvc.beaconDB.FinalizedState()
	if err != nil {
		t.Fatal(err)
	}
	newFinalizedBlock, err := chainSvc.beaconDB.FinalizedBlock()
	if err != nil {
		t.Fatal(err)
	}
	if newFinalizedState.Slot-genesisSlot != offset {
		t.Errorf("Wanted finalized state slot: %d, got: %d",
			offset, newFinalizedState.Slot-genesisSlot)
	}
	if newFinalizedBlock.Slot-genesisSlot != offset {
		t.Errorf("Wanted finalized block slot: %d, got: %d",
			offset, newFinalizedBlock.Slot-genesisSlot)
	}
}

func TestUpdateFFGCheckPts_NewJustifiedSkipSlot(t *testing.T) {
	genesisSlot := uint64(0)
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	chainSvc := setupBeaconChain(t, beaconDB, nil)
	gBlockRoot, gBlock, gState, privKeys := setupFFGTest(t)
	if err := chainSvc.beaconDB.SaveBlock(gBlock); err != nil {
		t.Fatal(err)
	}
	gState.LatestBlock = gBlock
	if err := chainSvc.beaconDB.UpdateChainHead(ctx, gBlock, gState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.SaveFinalizedState(gState); err != nil {
		t.Fatal(err)
	}

	// Last justified check point happened at slot 0.
	if err := chainSvc.beaconDB.SaveJustifiedBlock(
		&pb.BeaconBlock{Slot: genesisSlot}); err != nil {
		t.Fatal(err)
	}

	// Also saved finalized block to slot 0 to test justification case only.
	if err := chainSvc.beaconDB.SaveFinalizedBlock(
		&pb.BeaconBlock{Slot: genesisSlot}); err != nil {
		t.Fatal(err)
	}

	// New justified slot in state is at slot 64, but it's a skip slot...
	offset := uint64(64)
	lastAvailableSlot := uint64(60)
	proposerIdx, err := helpers.BeaconProposerIndex(gState)
	if err != nil {
		t.Fatal(err)
	}
	gState.CurrentJustifiedEpoch = 1
	gState.Slot = genesisSlot + offset
	buf := make([]byte, 32)
	binary.LittleEndian.PutUint64(buf, 0)
	domain := helpers.DomainVersion(gState, 0, params.BeaconConfig().DomainRandao)
	epochSignature := privKeys[proposerIdx].Sign(buf, domain)
	block := &pb.BeaconBlock{
		Slot:            genesisSlot + lastAvailableSlot,
		ParentBlockRoot: gBlockRoot[:],
		Body: &pb.BeaconBlockBody{
			RandaoReveal: epochSignature.Marshal(),
		}}
	if err := chainSvc.beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	computedState := &pb.BeaconState{Slot: genesisSlot + lastAvailableSlot}
	computedState.LatestBlock = block
	if err := chainSvc.beaconDB.SaveState(ctx, computedState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.beaconDB.UpdateChainHead(ctx, block, gState); err != nil {
		t.Fatal(err)
	}
	if err := chainSvc.updateFFGCheckPts(ctx, gState); err != nil {
		t.Fatal(err)
	}

	// Getting latest justification check point from DB and
	// verify they have been updated.
	newJustifiedState, err := chainSvc.beaconDB.JustifiedState()
	if err != nil {
		t.Fatal(err)
	}
	newJustifiedBlock, err := chainSvc.beaconDB.JustifiedBlock()
	if err != nil {
		t.Fatal(err)
	}
	if newJustifiedState.Slot-genesisSlot != lastAvailableSlot {
		t.Errorf("Wanted justification state slot: %d, got: %d",
			lastAvailableSlot, newJustifiedState.Slot-genesisSlot)
	}
	if newJustifiedBlock.Slot-genesisSlot != lastAvailableSlot {
		t.Errorf("Wanted justification block slot: %d, got: %d",
			offset, newJustifiedBlock.Slot-genesisSlot)
	}
}

func setupFFGTest(t *testing.T) ([32]byte, *pb.BeaconBlock, *pb.BeaconState, []*bls.SecretKey) {
	genesisSlot := uint64(0)
	var crosslinks []*pb.Crosslink
	for i := 0; i < int(params.BeaconConfig().ShardCount); i++ {
		crosslinks = append(crosslinks, &pb.Crosslink{Epoch: 0})
	}
	latestRandaoMixes := make(
		[][]byte,
		params.BeaconConfig().LatestRandaoMixesLength,
	)
	for i := 0; i < len(latestRandaoMixes); i++ {
		latestRandaoMixes[i] = make([]byte, 32)
	}
	var validatorRegistry []*pb.Validator
	var validatorBalances []uint64
	var privKeys []*bls.SecretKey
	for i := uint64(0); i < 64; i++ {
		priv, err := bls.RandKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		privKeys = append(privKeys, priv)
		validatorRegistry = append(validatorRegistry,
			&pb.Validator{
				Pubkey:    priv.PublicKey().Marshal(),
				ExitEpoch: params.BeaconConfig().FarFutureEpoch,
			})
		validatorBalances = append(validatorBalances, params.BeaconConfig().MaxDepositAmount)
	}
	gBlock := &pb.BeaconBlock{Slot: genesisSlot}
	gBlockRoot, err := hashutil.HashBeaconBlock(gBlock)
	if err != nil {
		t.Fatal(err)
	}
	gState := &pb.BeaconState{
		Slot:                   genesisSlot,
		LatestBlockRoots:       make([][]byte, params.BeaconConfig().LatestBlockRootsLength),
		LatestRandaoMixes:      latestRandaoMixes,
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
		LatestSlashedBalances:  make([]uint64, params.BeaconConfig().LatestSlashedExitLength),
		LatestCrosslinks:       crosslinks,
		ValidatorRegistry:      validatorRegistry,
		Balances:               validatorBalances,
		LatestBlock:            gBlock,
		Fork: &pb.Fork{
			PreviousVersion: params.BeaconConfig().GenesisForkVersion,
			CurrentVersion:  params.BeaconConfig().GenesisForkVersion,
			Epoch:           0,
		},
	}
	return gBlockRoot, gBlock, gState, privKeys
}

func TestVoteCount_CacheEnabledAndMiss(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	genesisBlock := b.NewGenesisBlock([]byte("stateroot"))
	genesisRoot, err := hashutil.HashBeaconBlock(genesisBlock)
	if err != nil {
		t.Fatal(err)
	}
	if err := beaconDB.SaveBlock(genesisBlock); err != nil {
		t.Fatal(err)
	}

	potentialHead := &pb.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: genesisRoot[:],
	}
	pHeadHash, err := hashutil.HashBeaconBlock(potentialHead)
	if err != nil {
		t.Fatal(err)
	}
	potentialHead2 := &pb.BeaconBlock{
		Slot:            6,
		ParentBlockRoot: genesisRoot[:],
	}
	pHeadHash2, err := hashutil.HashBeaconBlock(potentialHead2)
	if err != nil {
		t.Fatal(err)
	}
	// We store these potential heads in the DB.
	if err := beaconDB.SaveBlock(potentialHead); err != nil {
		t.Fatal(err)
	}
	if err := beaconDB.SaveBlock(potentialHead2); err != nil {
		t.Fatal(err)
	}
	beaconState := &pb.BeaconState{ValidatorRegistry: []*pb.Validator{{EffectiveBalance: 1e9}, {EffectiveBalance: 1e9}}}
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       potentialHead.Slot,
		BlockRoot:  pHeadHash[:],
		ParentRoot: potentialHead.ParentBlockRoot,
	}
	voteTargets[1] = &pb.AttestationTarget{
		Slot:       potentialHead2.Slot,
		BlockRoot:  pHeadHash2[:],
		ParentRoot: potentialHead2.ParentBlockRoot,
	}
	count, err := VoteCount(genesisBlock, beaconState, voteTargets, beaconDB)
	if err != nil {
		t.Fatalf("Could not fetch vote balances: %v", err)
	}
	if count != 2e9 {
		t.Errorf("Expected total balances 2e9, received %d", count)
	}

	// Verify block ancestor was correctly cached.
	h, _ := hashutil.HashBeaconBlock(potentialHead)
	cachedInfo, err := blkAncestorCache.AncestorBySlot(h[:], genesisBlock.Slot)
	if err != nil {
		t.Fatal(err)
	}
	// Verify the cached block ancestor is genesis block.
	if bytesutil.ToBytes32(cachedInfo.Target.BlockRoot) != genesisRoot {
		t.Error("could not retrieve the correct ancestor block")
	}
}

func TestVoteCount_CacheEnabledAndHit(t *testing.T) {
	t.Skip()
	genesisBlock := b.NewGenesisBlock([]byte("stateroot"))
	genesisRoot, err := hashutil.HashBeaconBlock(genesisBlock)
	if err != nil {
		t.Fatal(err)
	}

	potentialHead := &pb.BeaconBlock{
		Slot:            5,
		ParentBlockRoot: genesisRoot[:],
	}
	pHeadHash, _ := hashutil.HashBeaconBlock(potentialHead)
	potentialHead2 := &pb.BeaconBlock{
		Slot:            6,
		ParentBlockRoot: genesisRoot[:],
	}
	pHeadHash2, _ := hashutil.HashBeaconBlock(potentialHead2)

	beaconState := &pb.BeaconState{Balances: []uint64{1e9, 1e9}}
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:       potentialHead.Slot,
		BlockRoot:  pHeadHash[:],
		ParentRoot: potentialHead.ParentBlockRoot,
	}
	voteTargets[1] = &pb.AttestationTarget{
		Slot:       potentialHead2.Slot,
		BlockRoot:  pHeadHash2[:],
		ParentRoot: potentialHead2.ParentBlockRoot,
	}

	aInfo := &cache.AncestorInfo{
		Target: &pb.AttestationTarget{
			Slot:       genesisBlock.Slot,
			BlockRoot:  genesisRoot[:],
			ParentRoot: genesisBlock.ParentBlockRoot,
		},
	}
	// Presave cached ancestor blocks before running vote count.
	if err := blkAncestorCache.AddBlockAncestor(aInfo); err != nil {
		t.Fatal(err)
	}
	aInfo.Target.BlockRoot = pHeadHash2[:]
	if err := blkAncestorCache.AddBlockAncestor(aInfo); err != nil {
		t.Fatal(err)
	}

	count, err := VoteCount(genesisBlock, beaconState, voteTargets, nil)
	if err != nil {
		t.Fatalf("Could not fetch vote balances: %v", err)
	}
	if count != 2e9 {
		t.Errorf("Expected total balances 2e9, received %d", count)
	}
}
