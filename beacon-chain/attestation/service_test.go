package attestation

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/prysmaticlabs/prysm/beacon-chain/cache"
	"github.com/prysmaticlabs/prysm/beacon-chain/internal"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	"github.com/sirupsen/logrus"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
}

var _ = TargetHandler(&Service{})

func TestUpdateLatestAttestation_UpdatesLatest(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	var validators []*pb.Validator
	for i := 0; i < 64; i++ {
		validators = append(validators, &pb.Validator{
			Pubkey:          []byte{byte(i)},
			ActivationEpoch: 0,
			ExitEpoch:       10,
		})
	}

	beaconState := &pb.BeaconState{
		Slot:                   1,
		ValidatorRegistry:      validators,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	block := &pb.BeaconBlock{
		Slot: 1,
	}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatal(err)
	}
	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})

	attestation := &pb.Attestation{
		AggregationBitfield: []byte{0x01},
		Data: &pb.AttestationData{
			Slot:  1,
			Shard: 2,
		},
	}

	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
	pubkey := bytesutil.ToBytes48(beaconState.ValidatorRegistry[47].Pubkey)
	if service.store.m[pubkey].Data.Slot !=
		attestation.Data.Slot {
		t.Errorf("Incorrect slot stored, wanted: %d, got: %d",
			attestation.Data.Slot, service.store.m[pubkey].Data.Slot)
	}

	beaconState = &pb.BeaconState{
		Slot:                   36,
		ValidatorRegistry:      validators,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatalf("could not save state: %v", err)
	}

	attestation.Data.Slot = 36
	attestation.Data.Shard = 36
	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
	if service.store.m[pubkey].Data.Slot !=
		attestation.Data.Slot {
		t.Errorf("Incorrect slot stored, wanted: %d, got: %d",
			attestation.Data.Slot, service.store.m[pubkey].Data.Slot)
	}
}

func TestAttestationPool_UpdatesAttestationPool(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	var validators []*pb.Validator
	for i := 0; i < 64; i++ {
		validators = append(validators, &pb.Validator{
			Pubkey:          []byte{byte(i)},
			ActivationEpoch: 0,
			ExitEpoch:       10,
		})
	}
	beaconState := &pb.BeaconState{
		Slot:                   1,
		ValidatorRegistry:      validators,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	block := &pb.BeaconBlock{
		Slot: 1,
	}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatal(err)
	}

	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})
	attestation := &pb.Attestation{
		AggregationBitfield: []byte{0x01},
		Data: &pb.AttestationData{
			Slot:  1,
			Shard: 1,
		},
	}

	if err := service.handleAttestation(context.Background(), attestation); err != nil {
		t.Error(err)
	}
}

func TestLatestAttestationTarget_CantGetAttestation(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	if err := beaconDB.SaveState(ctx, &pb.BeaconState{
		ValidatorRegistry: []*pb.Validator{{}},
		LatestBlock:       &pb.BeaconBlock{Slot: 0},
	}); err != nil {
		t.Fatalf("could not save state: %v", err)
	}
	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})
	headState, err := beaconDB.HeadState(ctx)
	if err != nil {
		t.Fatal(err)
	}

	index := uint64(100)
	want := fmt.Sprintf("invalid validator index %d", index)
	if _, err := service.LatestAttestationTarget(headState, index); !strings.Contains(err.Error(), want) {
		t.Errorf("Wanted error to contain %s, received %v", want, err)
	}
}

func TestLatestAttestationTarget_ReturnsLatestAttestedBlock(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	pubKey := []byte{'A'}
	if err := beaconDB.SaveState(ctx, &pb.BeaconState{
		ValidatorRegistry: []*pb.Validator{{Pubkey: pubKey}},
		LatestBlock:       &pb.BeaconBlock{Slot: 0},
	}); err != nil {
		t.Fatalf("could not save state: %v", err)
	}

	block := &pb.BeaconBlock{Slot: 999}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatalf("could not save block: %v", err)
	}
	blockRoot, err := hashutil.HashBeaconBlock(block)
	if err != nil {
		log.Fatalf("could not hash block: %v", err)
	}
	if err := beaconDB.SaveAttestationTarget(ctx, &pb.AttestationTarget{
		Slot:       block.Slot,
		BlockRoot:  blockRoot[:],
		ParentRoot: []byte{},
	}); err != nil {
		log.Fatalf("could not save att target: %v", err)
	}

	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})

	attestation := &pb.Attestation{
		Data: &pb.AttestationData{
			BeaconBlockRootHash32: blockRoot[:],
		}}
	pubKey48 := bytesutil.ToBytes48(pubKey)
	service.store.m[pubKey48] = attestation

	headState, err := beaconDB.HeadState(ctx)
	if err != nil {
		t.Fatal(err)
	}

	latestAttestedTarget, err := service.LatestAttestationTarget(headState, 0)
	if err != nil {
		t.Fatalf("Could not get latest attestation: %v", err)
	}

	if !bytes.Equal(blockRoot[:], latestAttestedTarget.BlockRoot) {
		t.Errorf("Wanted: %v, got: %v", blockRoot[:], latestAttestedTarget.BlockRoot)
	}
}

func TestUpdateLatestAttestation_CacheEnabledAndMiss(t *testing.T) {
	t.Skip()
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	var validators []*pb.Validator
	for i := 0; i < 64; i++ {
		validators = append(validators, &pb.Validator{
			Pubkey:          []byte{byte(i)},
			ActivationEpoch: 0,
			ExitEpoch:       10,
		})
	}

	beaconState := &pb.BeaconState{
		Slot:                   1,
		ValidatorRegistry:      validators,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	block := &pb.BeaconBlock{
		Slot: 1,
	}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatal(err)
	}
	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})

	attestation := &pb.Attestation{
		AggregationBitfield: []byte{0x01},
		Data: &pb.AttestationData{
			Slot:  1,
			Shard: 2,
		},
	}

	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
	pubkey := bytesutil.ToBytes48(beaconState.ValidatorRegistry[60].Pubkey)
	if service.store.m[pubkey].Data.Slot !=
		attestation.Data.Slot {
		t.Errorf("Incorrect slot stored, wanted: %d, got: %d",
			attestation.Data.Slot, service.store.m[pubkey].Data.Slot)
	}

	attestation.Data.Slot = 36
	attestation.Data.Shard = 36

	beaconState = &pb.BeaconState{
		Slot:                   36,
		ValidatorRegistry:      validators,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatalf("could not save state: %v", err)
	}

	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
	if service.store.m[pubkey].Data.Slot !=
		attestation.Data.Slot {
		t.Errorf("Incorrect slot stored, wanted: %d, got: %d",
			attestation.Data.Slot, service.store.m[pubkey].Data.Slot)
	}

	// Verify the committee for attestation's data slot was cached.
	fetchedCommittees, err := committeeCache.CommitteesInfoBySlot(attestation.Data.Slot)
	if err != nil {
		t.Fatal(err)
	}
	wantedCommittee := []uint64{12}
	if !reflect.DeepEqual(wantedCommittee, fetchedCommittees.Committees[0].Committee) {
		t.Errorf(
			"Result indices was an unexpected value. Wanted %d, got %d",
			wantedCommittee,
			fetchedCommittees.Committees[0].Committee,
		)
	}
}

func TestUpdateLatestAttestation_CacheEnabledAndHit(t *testing.T) {
	t.Skip()
	var validators []*pb.Validator
	for i := 0; i < 64; i++ {
		validators = append(validators, &pb.Validator{
			Pubkey:          []byte{byte(i)},
			ActivationEpoch: 0,
			ExitEpoch:       10,
		})
	}

	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	beaconState := &pb.BeaconState{
		Slot:                   2,
		ValidatorRegistry:      validators,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
	}
	block := &pb.BeaconBlock{
		Slot: 2,
	}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatal(err)
	}

	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})

	slot := uint64(2)
	shard := uint64(3)
	index := uint64(4)
	attestation := &pb.Attestation{
		AggregationBitfield: []byte{0x01},
		Data: &pb.AttestationData{
			Slot:  slot,
			Shard: shard,
		},
	}

	csInSlot := &cache.CommitteesInSlot{
		Slot: slot,
		Committees: []*cache.CommitteeInfo{
			{Shard: shard, Committee: []uint64{index, 999}},
		}}

	if err := committeeCache.AddCommittees(csInSlot); err != nil {
		t.Fatal(err)
	}

	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
	pubkey := bytesutil.ToBytes48([]byte{byte(index)})
	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}

	if service.store.m[pubkey].Data.Slot !=
		attestation.Data.Slot {
		t.Errorf("Incorrect slot stored, wanted: %d, got: %d",
			attestation.Data.Slot, service.store.m[pubkey].Data.Slot)
	}
}

func TestUpdateLatestAttestation_InvalidIndex(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	hook := logTest.NewGlobal()
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	var validators []*pb.Validator
	for i := 0; i < 64; i++ {
		validators = append(validators, &pb.Validator{
			Pubkey:          []byte{byte(i)},
			ActivationEpoch: 0,
			ExitEpoch:       10,
		})
	}

	beaconState := &pb.BeaconState{
		Slot:                   1,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
		ValidatorRegistry:      validators,
	}
	block := &pb.BeaconBlock{
		Slot: 1,
	}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatal(err)
	}
	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})
	attestation := &pb.Attestation{
		AggregationBitfield: []byte{0xC0},
		Data: &pb.AttestationData{
			Slot:  1,
			Shard: 1,
		},
	}

	if err := service.UpdateLatestAttestation(ctx, attestation); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
	testutil.AssertLogsContain(t, hook, "Bitfield points to an invalid index in the committee")
}

func TestUpdateLatestAttestation_BatchUpdate(t *testing.T) {
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)
	ctx := context.Background()

	var validators []*pb.Validator
	for i := 0; i < 64; i++ {
		validators = append(validators, &pb.Validator{
			Pubkey:          []byte{byte(i)},
			ActivationEpoch: 0,
			ExitEpoch:       10,
		})
	}

	beaconState := &pb.BeaconState{
		Slot:                   1,
		LatestRandaoMixes:      make([][]byte, params.BeaconConfig().LatestRandaoMixesLength),
		LatestActiveIndexRoots: make([][]byte, params.BeaconConfig().LatestActiveIndexRootsLength),
		ValidatorRegistry:      validators,
	}
	block := &pb.BeaconBlock{
		Slot: 1,
	}
	if err := beaconDB.SaveBlock(block); err != nil {
		t.Fatal(err)
	}
	beaconState.LatestBlock = block
	if err := beaconDB.UpdateChainHead(ctx, block, beaconState); err != nil {
		t.Fatal(err)
	}
	service := NewAttestationService(context.Background(), &Config{BeaconDB: beaconDB})
	attestations := make([]*pb.Attestation, 0)
	for i := 0; i < 10; i++ {
		attestations = append(attestations, &pb.Attestation{
			AggregationBitfield: []byte{0x80},
			Data: &pb.AttestationData{
				Slot:  1,
				Shard: 1,
			},
		})
	}

	if err := service.BatchUpdateLatestAttestation(ctx, attestations); err != nil {
		t.Fatalf("could not update latest attestation: %v", err)
	}
}
