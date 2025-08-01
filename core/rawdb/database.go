// Copyright 2018 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package rawdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/olekukonko/tablewriter"
)

// freezerdb is a database wrapper that enables ancient chain segment freezing.
type freezerdb struct {
	ethdb.KeyValueStore
	ethdb.AncientStore

	readOnly    bool
	ancientRoot string

	ethdb.AncientFreezer
	stateStore ethdb.Database
}

func (frdb *freezerdb) StateStoreReader() ethdb.Reader {
	if frdb.stateStore == nil {
		return frdb
	}
	return frdb.stateStore
}

// AncientDatadir returns the path of root ancient directory.
func (frdb *freezerdb) AncientDatadir() (string, error) {
	return frdb.ancientRoot, nil
}

// Close implements io.Closer, closing both the fast key-value store as well as
// the slow ancient tables.
func (frdb *freezerdb) Close() error {
	var errs []error
	if err := frdb.AncientStore.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := frdb.KeyValueStore.Close(); err != nil {
		errs = append(errs, err)
	}
	if frdb.HasSeparateStateStore() {
		if err := frdb.GetStateStore().Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

func (frdb *freezerdb) SetStateStore(state ethdb.Database) {
	if frdb.stateStore != nil {
		frdb.stateStore.Close()
	}
	frdb.stateStore = state
}

func (frdb *freezerdb) GetStateStore() ethdb.Database {
	if frdb.stateStore != nil {
		return frdb.stateStore
	}
	return frdb
}

func (frdb *freezerdb) HasSeparateStateStore() bool {
	return frdb.stateStore != nil
}

// Freeze is a helper method used for external testing to trigger and block until
// a freeze cycle completes, without having to sleep for a minute to trigger the
// automatic background run.
func (frdb *freezerdb) Freeze(threshold uint64) error {
	if frdb.readOnly {
		return errReadOnly
	}
	// Set the freezer threshold to a temporary value
	defer func(old uint64) {
		frdb.AncientStore.(*chainFreezer).threshold.Store(old)
	}(frdb.AncientStore.(*chainFreezer).threshold.Load())
	frdb.AncientStore.(*chainFreezer).threshold.Store(threshold)
	// Trigger a freeze cycle and block until it's done
	trigger := make(chan struct{}, 1)
	frdb.AncientStore.(*chainFreezer).trigger <- trigger
	<-trigger
	return nil
}

func (frdb *freezerdb) SetupFreezerEnv(env *ethdb.FreezerEnv, blockHistory uint64) error {
	return frdb.AncientFreezer.SetupFreezerEnv(env, blockHistory)
}

// nofreezedb is a database wrapper that disables freezer data retrievals.
type nofreezedb struct {
	ethdb.KeyValueStore
	stateStore ethdb.Database
}

// HasAncient returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) HasAncient(kind string, number uint64) (bool, error) {
	return false, errNotSupported
}

// Ancient returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) Ancient(kind string, number uint64) ([]byte, error) {
	return nil, errNotSupported
}

// AncientRange returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) AncientRange(kind string, start, max, maxByteSize uint64) ([][]byte, error) {
	return nil, errNotSupported
}

// Ancients returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) Ancients() (uint64, error) {
	return 0, errNotSupported
}

// ItemAmountInAncient returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) ItemAmountInAncient() (uint64, error) {
	return 0, errNotSupported
}

// Tail returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) Tail() (uint64, error) {
	return 0, errNotSupported
}

// AncientSize returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) AncientSize(kind string) (uint64, error) {
	return 0, errNotSupported
}

// ModifyAncients is not supported.
func (db *nofreezedb) ModifyAncients(func(ethdb.AncientWriteOp) error) (int64, error) {
	return 0, errNotSupported
}

// TruncateHead returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) TruncateHead(items uint64) (uint64, error) {
	return 0, errNotSupported
}

// TruncateTail returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) TruncateTail(items uint64) (uint64, error) {
	return 0, errNotSupported
}

// TruncateTableTail will truncate certain table to new tail
func (db *nofreezedb) TruncateTableTail(kind string, tail uint64) (uint64, error) {
	return 0, errNotSupported
}

// ResetTable will reset certain table with new start point
func (db *nofreezedb) ResetTable(kind string, startAt uint64, onlyEmpty bool) error {
	return errNotSupported
}

// SyncAncient returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) SyncAncient() error {
	return errNotSupported
}

func (db *nofreezedb) SetStateStore(state ethdb.Database) {
	db.stateStore = state
}

func (db *nofreezedb) GetStateStore() ethdb.Database {
	if db.stateStore != nil {
		return db.stateStore
	}
	return db
}

func (db *nofreezedb) HasSeparateStateStore() bool {
	return db.stateStore != nil
}

func (db *nofreezedb) StateStoreReader() ethdb.Reader {
	if db.stateStore != nil {
		return db.stateStore
	}
	return db
}

func (db *nofreezedb) ReadAncients(fn func(reader ethdb.AncientReaderOp) error) (err error) {
	// Unlike other ancient-related methods, this method does not return
	// errNotSupported when invoked.
	// The reason for this is that the caller might want to do several things:
	// 1. Check if something is in the freezer,
	// 2. If not, check leveldb.
	//
	// This will work, since the ancient-checks inside 'fn' will return errors,
	// and the leveldb work will continue.
	//
	// If we instead were to return errNotSupported here, then the caller would
	// have to explicitly check for that, having an extra clause to do the
	// non-ancient operations.
	return fn(db)
}

func (db *nofreezedb) AncientOffSet() uint64 {
	return 0
}

// AncientDatadir returns an error as we don't have a backing chain freezer.
func (db *nofreezedb) AncientDatadir() (string, error) {
	return "", errNotSupported
}

func (db *nofreezedb) SetupFreezerEnv(env *ethdb.FreezerEnv, blockHistory uint64) error {
	return nil
}

// NewDatabase creates a high level database on top of a given key-value data
// store without a freezer moving immutable chain segments into cold storage.
func NewDatabase(db ethdb.KeyValueStore) ethdb.Database {
	return &nofreezedb{KeyValueStore: db}
}

type emptyfreezedb struct {
	ethdb.KeyValueStore
}

// HasAncient returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) HasAncient(kind string, number uint64) (bool, error) {
	return false, nil
}

// Ancient returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) Ancient(kind string, number uint64) ([]byte, error) {
	return nil, nil
}

// AncientRange returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) AncientRange(kind string, start, max, maxByteSize uint64) ([][]byte, error) {
	return nil, nil
}

// Ancients returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) Ancients() (uint64, error) {
	return 0, nil
}

// ItemAmountInAncient returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) ItemAmountInAncient() (uint64, error) {
	return 0, nil
}

// Tail returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) Tail() (uint64, error) {
	return 0, nil
}

// AncientSize returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) AncientSize(kind string) (uint64, error) {
	return 0, nil
}

// ModifyAncients returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) ModifyAncients(func(ethdb.AncientWriteOp) error) (int64, error) {
	return 0, nil
}

// TruncateHead returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) TruncateHead(items uint64) (uint64, error) {
	return 0, nil
}

// TruncateTail returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) TruncateTail(items uint64) (uint64, error) {
	return 0, nil
}

// TruncateTableTail returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) TruncateTableTail(kind string, tail uint64) (uint64, error) {
	return 0, nil
}

// ResetTable returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) ResetTable(kind string, startAt uint64, onlyEmpty bool) error {
	return nil
}

// SyncAncient returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) SyncAncient() error {
	return nil
}

func (db *emptyfreezedb) GetStateStore() ethdb.Database      { return db }
func (db *emptyfreezedb) SetStateStore(state ethdb.Database) {}
func (db *emptyfreezedb) StateStoreReader() ethdb.Reader     { return db }
func (db *emptyfreezedb) HasSeparateStateStore() bool        { return false }
func (db *emptyfreezedb) ReadAncients(fn func(reader ethdb.AncientReaderOp) error) (err error) {
	return nil
}
func (db *emptyfreezedb) AncientOffSet() uint64 { return 0 }

// AncientDatadir returns nil for pruned db that we don't have a backing chain freezer.
func (db *emptyfreezedb) AncientDatadir() (string, error) {
	return "", nil
}
func (db *emptyfreezedb) SetupFreezerEnv(env *ethdb.FreezerEnv, blockHistory uint64) error {
	return nil
}

// NewEmptyFreezeDB is used for CLI such as `geth db inspect` in pruned db that we don't
// have a backing chain freezer.
// WARNING: it must be only used in the above case.
func NewEmptyFreezeDB(db ethdb.KeyValueStore) ethdb.Database {
	return &emptyfreezedb{KeyValueStore: db}
}

// NewFreezerDb only create a freezer without statedb.
func NewFreezerDb(db ethdb.KeyValueStore, frz, namespace string, readonly bool, newOffSet uint64) (*Freezer, error) {
	// Create the idle freezer instance, this operation should be atomic to avoid mismatch between offset and acientDB.
	frdb, err := NewFreezer(frz, namespace, readonly, freezerTableSize, chainFreezerNoSnappy)
	if err != nil {
		return nil, err
	}
	return frdb, nil
}

// resolveChainFreezerDir is a helper function which resolves the absolute path
// of chain freezer by considering backward compatibility.
//
// rules:
// 1. in path mode, block data is stored in chain dir and state data is in state dir.
// 2. in hash mode, block data is stored in chain dir or ancient dir(before big merge), no state dir.
func resolveChainFreezerDir(ancient string) string {
	// Check if the chain freezer is already present in the specified
	// sub folder, if not then two possibilities:
	// - chain freezer is not initialized
	// - chain freezer exists in legacy location (root ancient folder)
	chain := filepath.Join(ancient, ChainFreezerName)
	state := filepath.Join(ancient, MerkleStateFreezerName)
	if common.FileExist(chain) {
		return chain
	}
	if common.FileExist(state) {
		return chain
	}
	if common.FileExist(ancient) {
		log.Info("Found legacy ancient chain path", "location", ancient)
		chain = ancient
	}
	return chain
}

// NewDatabaseWithFreezer creates a high level database on top of a given key-
// value data store with a freezer moving immutable chain segments into cold
// storage. The passed ancient indicates the path of root ancient directory
// where the chain freezer can be opened.
func NewDatabaseWithFreezer(db ethdb.KeyValueStore, ancient string, namespace string, readonly, disableFreeze, multiDatabase bool) (ethdb.Database, error) {
	// Create the idle freezer instance. If the given ancient directory is empty,
	// in-memory chain freezer is used (e.g. dev mode); otherwise the regular
	// file-based freezer is created.
	chainFreezerDir := ancient
	if chainFreezerDir != "" {
		chainFreezerDir = resolveChainFreezerDir(chainFreezerDir)
	}

	// if there has legacy offset, try to clean & reset the freezer metadata
	if legacyOffset := ReadLegacyOffset(db); legacyOffset > 0 {
		log.Info("Found legacy offset in freezerDB, will reset freezer meta", "offset", legacyOffset)
		if err := resetFreezerMeta(chainFreezerDir, namespace, legacyOffset); err != nil {
			return nil, err
		}
		CleanLegacyOffset(db)
	}

	// Create the idle freezer instance
	frdb, err := newChainFreezer(chainFreezerDir, namespace, readonly, multiDatabase)

	// We are creating the freezerdb here because the validation logic for db and freezer below requires certain interfaces
	// that need a database type. Therefore, we are pre-creating it for subsequent use.
	freezerDb := &freezerdb{
		ancientRoot:    ancient,
		KeyValueStore:  db,
		AncientStore:   frdb,
		AncientFreezer: frdb,
	}
	if err != nil {
		printChainMetadata(freezerDb)
		return nil, err
	}

	// Since the freezer can be stored separately from the user's key-value database,
	// there's a fairly high probability that the user requests invalid combinations
	// of the freezer and database. Ensure that we don't shoot ourselves in the foot
	// by serving up conflicting data, leading to both datastores getting corrupted.
	//
	//   - If both the freezer and key-value store are empty (no genesis), we just
	//     initialized a new empty freezer, so everything's fine.
	//   - If the key-value store is empty, but the freezer is not, we need to make
	//     sure the user's genesis matches the freezer. That will be checked in the
	//     blockchain, since we don't have the genesis block here (nor should we at
	//     this point care, the key-value/freezer combo is valid).
	//   - If neither the key-value store nor the freezer is empty, cross validate
	//     the genesis hashes to make sure they are compatible. If they are, also
	//     ensure that there's no gap between the freezer and subsequently leveldb.
	//   - If the key-value store is not empty, but the freezer is, we might just be
	//     upgrading to the freezer release, or we might have had a small chain and
	//     not frozen anything yet. Ensure that no blocks are missing yet from the
	//     key-value store, since that would mean we already had an old freezer.

	// If the genesis hash is empty, we have a new key-value store, so nothing to
	// validate in this method. If, however, the genesis hash is not nil, compare
	// it to the freezer content.
	// Only to check the followings when offset/ancientTail equal to 0, otherwise the block number
	// in ancientdb did not start with 0, no genesis block in ancientdb as well.
	ancientTail, err := frdb.Tail()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve Tail from ancient %v", err)
	}
	if kvgenesis, _ := db.Get(headerHashKey(0)); ancientTail == 0 && len(kvgenesis) > 0 {
		if frozen, _ := frdb.Ancients(); frozen > 0 {
			// If the freezer already contains something, ensure that the genesis blocks
			// match, otherwise we might mix up freezers across chains and destroy both
			// the freezer and the key-value store.
			frgenesis, err := frdb.Ancient(ChainFreezerHashTable, 0)
			if err != nil {
				printChainMetadata(freezerDb)
				return nil, fmt.Errorf("failed to retrieve genesis from ancient %v", err)
			} else if !bytes.Equal(kvgenesis, frgenesis) {
				printChainMetadata(freezerDb)
				return nil, fmt.Errorf("genesis mismatch: %#x (leveldb) != %#x (ancients)", kvgenesis, frgenesis)
			}
			// Key-value store and freezer belong to the same network. Ensure that they
			// are contiguous, otherwise we might end up with a non-functional freezer.
			if kvhash, _ := db.Get(headerHashKey(frozen)); len(kvhash) == 0 {
				// Subsequent header after the freezer limit is missing from the database.
				// Reject startup if the database has a more recent head.
				if head := *ReadHeaderNumber(freezerDb, ReadHeadHeaderHash(freezerDb)); head > frozen-1 {
					// Find the smallest block stored in the key-value store
					// in range of [frozen, head]
					var number uint64
					for number = frozen; number <= head; number++ {
						if present, _ := db.Has(headerHashKey(number)); present {
							break
						}
					}
					// We are about to exit on error. Print database metadata before exiting
					printChainMetadata(freezerDb)
					return nil, fmt.Errorf("gap in the chain between ancients [0 - #%d] and leveldb [#%d - #%d] ",
						frozen-1, number, head)
				}
				// Database contains only older data than the freezer, this happens if the
				// state was wiped and reinited from an existing freezer.
			}
			// Otherwise, key-value store continues where the freezer left off, all is fine.
			// We might have duplicate blocks (crash after freezer write but before key-value
			// store deletion, but that's fine).
		} else {
			// If the freezer is empty, ensure nothing was moved yet from the key-value
			// store, otherwise we'll end up missing data. We check block #1 to decide
			// if we froze anything previously or not, but do take care of databases with
			// only the genesis block.
			if ReadHeadHeaderHash(freezerDb) != common.BytesToHash(kvgenesis) {
				// Key-value store contains more data than the genesis block, make sure we
				// didn't freeze anything yet.
				if kvblob, _ := db.Get(headerHashKey(1)); len(kvblob) == 0 {
					printChainMetadata(freezerDb)
					return nil, errors.New("ancient chain segments already extracted, please set --datadir.ancient to the correct path")
				}
				// Block #1 is still in the database, we're allowed to init a new freezer
			}
			// Otherwise, the head header is still the genesis, we're allowed to init a new
			// freezer.
		}
	}

	// Freezer is consistent with the key-value database, permit combining the two
	if !disableFreeze && !readonly {
		frdb.wg.Add(1)
		go func() {
			frdb.freeze(db, false)
			frdb.wg.Done()
		}()
	}
	return freezerDb, nil
}

// NewMemoryDatabase creates an ephemeral in-memory key-value database without a
// freezer moving immutable chain segments into cold storage.
func NewMemoryDatabase() ethdb.Database {
	return NewDatabase(memorydb.New())
}

const (
	DBPebble  = "pebble"
	DBLeveldb = "leveldb"
)

// PreexistingDatabase checks the given data directory whether a database is already
// instantiated at that location, and if so, returns the type of database (or the
// empty string).
func PreexistingDatabase(path string) string {
	if _, err := os.Stat(filepath.Join(path, "CURRENT")); err != nil {
		return "" // No pre-existing db
	}
	if matches, err := filepath.Glob(filepath.Join(path, "OPTIONS*")); len(matches) > 0 || err != nil {
		if err != nil {
			panic(err) // only possible if the pattern is malformed
		}
		return DBPebble
	}
	return DBLeveldb
}

type counter uint64

func (c counter) String() string {
	return fmt.Sprintf("%d", c)
}

func (c counter) Percentage(current uint64) string {
	return fmt.Sprintf("%d", current*100/uint64(c))
}

// stat stores sizes and count for a parameter
type stat struct {
	size  common.StorageSize
	count counter
}

// Add size to the stat and increase the counter by 1
func (s *stat) Add(size common.StorageSize) {
	s.size += size
	s.count++
}

func (s *stat) Size() string {
	return s.size.String()
}

func (s *stat) Count() string {
	return s.count.String()
}

func AncientInspect(db ethdb.Database) error {
	ancientTail, err := db.Tail()
	if err != nil {
		return err
	}
	ancientHead, err := db.Ancients()
	if err != nil {
		return err
	}
	stats := [][]string{
		{"Offset/StartBlockNumber", "Offset/StartBlockNumber of ancientDB", counter(ancientTail).String()},
		{"Amount of remained items in AncientStore", "Remaining items of ancientDB", counter(ancientHead - ancientTail).String()},
		{"The last BlockNumber within ancientDB", "The last BlockNumber", counter(ancientHead - 1).String()},
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Database", "Category", "Items"})
	table.SetFooter([]string{"", "AncientStore information", ""})
	table.AppendBulk(stats)
	table.Render()

	return nil
}

func PruneHashTrieNodeInDataBase(db ethdb.Database) error {
	it := db.NewIterator([]byte{}, []byte{})
	defer it.Release()

	total_num := 0
	for it.Next() {
		var key = it.Key()
		switch {
		case IsLegacyTrieNode(key, it.Value()):
			db.Delete(key)
			total_num++
			if total_num%100000 == 0 {
				log.Info("Pruning hash-base state trie nodes", "Complete progress: ", total_num)
			}
		default:
			continue
		}
	}
	log.Info("Pruning hash-base state trie nodes", "Complete progress", total_num)
	return nil
}

type DataType int

const (
	StateDataType DataType = iota
	ChainDataType
	Unknown
)

func DataTypeByKey(key []byte) DataType {
	switch {
	// state
	case IsLegacyTrieNode(key, key),
		bytes.HasPrefix(key, stateIDPrefix) && len(key) == len(stateIDPrefix)+common.HashLength,
		IsAccountTrieNode(key),
		IsStorageTrieNode(key):
		return StateDataType

	default:
		for _, meta := range [][]byte{
			fastTrieProgressKey, persistentStateIDKey, trieJournalKey, snapSyncStatusFlagKey} {
			if bytes.Equal(key, meta) {
				return StateDataType
			}
		}
		return ChainDataType
	}
}

// InspectDatabase traverses the entire database and checks the size
// of all different categories of data.
func InspectDatabase(db ethdb.Database, keyPrefix, keyStart []byte) error {
	it := db.NewIterator(keyPrefix, keyStart)
	defer it.Release()

	var trieIter ethdb.Iterator
	if db.HasSeparateStateStore() {
		trieIter = db.GetStateStore().NewIterator(keyPrefix, nil)
		defer trieIter.Release()
	}

	var (
		count  int64
		start  = time.Now()
		logged = time.Now()

		// Key-value store statistics
		headers         stat
		bodies          stat
		receipts        stat
		tds             stat
		numHashPairings stat
		blobSidecars    stat
		hashNumPairings stat
		legacyTries     stat
		stateLookups    stat
		accountTries    stat
		storageTries    stat
		codes           stat
		txLookups       stat
		accountSnaps    stat
		storageSnaps    stat
		preimages       stat
		bloomBits       stat
		cliqueSnaps     stat
		parliaSnaps     stat

		// Verkle statistics
		verkleTries        stat
		verkleStateLookups stat

		// Les statistic
		chtTrieNodes   stat
		bloomTrieNodes stat

		// Meta- and unaccounted data
		metadata    stat
		unaccounted stat

		// Totals
		total common.StorageSize
	)
	// Inspect key-value database first.
	for it.Next() {
		var (
			key  = it.Key()
			size = common.StorageSize(len(key) + len(it.Value()))
		)
		total += size
		switch {
		case bytes.HasPrefix(key, headerPrefix) && len(key) == (len(headerPrefix)+8+common.HashLength):
			headers.Add(size)
		case bytes.HasPrefix(key, blockBodyPrefix) && len(key) == (len(blockBodyPrefix)+8+common.HashLength):
			bodies.Add(size)
		case bytes.HasPrefix(key, blockReceiptsPrefix) && len(key) == (len(blockReceiptsPrefix)+8+common.HashLength):
			receipts.Add(size)
		case IsLegacyTrieNode(key, it.Value()):
			legacyTries.Add(size)
		case bytes.HasPrefix(key, headerPrefix) && bytes.HasSuffix(key, headerTDSuffix):
			tds.Add(size)
		case bytes.HasPrefix(key, BlockBlobSidecarsPrefix):
			blobSidecars.Add(size)
		case bytes.HasPrefix(key, headerPrefix) && bytes.HasSuffix(key, headerHashSuffix):
			numHashPairings.Add(size)
		case bytes.HasPrefix(key, headerNumberPrefix) && len(key) == (len(headerNumberPrefix)+common.HashLength):
			hashNumPairings.Add(size)
		case bytes.HasPrefix(key, stateIDPrefix) && len(key) == len(stateIDPrefix)+common.HashLength:
			stateLookups.Add(size)
		case IsAccountTrieNode(key):
			accountTries.Add(size)
		case IsStorageTrieNode(key):
			storageTries.Add(size)
		case bytes.HasPrefix(key, CodePrefix) && len(key) == len(CodePrefix)+common.HashLength:
			codes.Add(size)
		case bytes.HasPrefix(key, txLookupPrefix) && len(key) == (len(txLookupPrefix)+common.HashLength):
			txLookups.Add(size)
		case bytes.HasPrefix(key, SnapshotAccountPrefix) && len(key) == (len(SnapshotAccountPrefix)+common.HashLength):
			accountSnaps.Add(size)
		case bytes.HasPrefix(key, SnapshotStoragePrefix) && len(key) == (len(SnapshotStoragePrefix)+2*common.HashLength):
			storageSnaps.Add(size)
		case bytes.HasPrefix(key, PreimagePrefix) && len(key) == (len(PreimagePrefix)+common.HashLength):
			preimages.Add(size)
		case bytes.HasPrefix(key, configPrefix) && len(key) == (len(configPrefix)+common.HashLength):
			metadata.Add(size)
		case bytes.HasPrefix(key, genesisPrefix) && len(key) == (len(genesisPrefix)+common.HashLength):
			metadata.Add(size)
		case bytes.HasPrefix(key, bloomBitsPrefix) && len(key) == (len(bloomBitsPrefix)+10+common.HashLength):
			bloomBits.Add(size)
		case bytes.HasPrefix(key, BloomBitsIndexPrefix):
			bloomBits.Add(size)
		case bytes.HasPrefix(key, CliqueSnapshotPrefix) && len(key) == 7+common.HashLength:
			cliqueSnaps.Add(size)
		case bytes.HasPrefix(key, ParliaSnapshotPrefix) && len(key) == 7+common.HashLength:
			parliaSnaps.Add(size)
		case bytes.HasPrefix(key, ChtTablePrefix) ||
			bytes.HasPrefix(key, ChtIndexTablePrefix) ||
			bytes.HasPrefix(key, ChtPrefix): // Canonical hash trie
			chtTrieNodes.Add(size)
		case bytes.HasPrefix(key, BloomTrieTablePrefix) ||
			bytes.HasPrefix(key, BloomTrieIndexPrefix) ||
			bytes.HasPrefix(key, BloomTriePrefix): // Bloomtrie sub
			bloomTrieNodes.Add(size)

		// Verkle trie data is detected, determine the sub-category
		case bytes.HasPrefix(key, VerklePrefix):
			remain := key[len(VerklePrefix):]
			switch {
			case IsAccountTrieNode(remain):
				verkleTries.Add(size)
			case bytes.HasPrefix(remain, stateIDPrefix) && len(remain) == len(stateIDPrefix)+common.HashLength:
				verkleStateLookups.Add(size)
			case bytes.Equal(remain, persistentStateIDKey):
				metadata.Add(size)
			case bytes.Equal(remain, trieJournalKey):
				metadata.Add(size)
			case bytes.Equal(remain, snapSyncStatusFlagKey):
				metadata.Add(size)
			default:
				unaccounted.Add(size)
			}
		default:
			var accounted bool
			for _, meta := range [][]byte{
				databaseVersionKey, headHeaderKey, headBlockKey, headFastBlockKey,
				lastPivotKey, fastTrieProgressKey, snapshotDisabledKey, SnapshotRootKey, snapshotJournalKey,
				snapshotGeneratorKey, snapshotRecoveryKey, txIndexTailKey, fastTxLookupLimitKey,
				uncleanShutdownKey, badBlockKey, transitionStatusKey, skeletonSyncStatusKey,
				persistentStateIDKey, trieJournalKey, snapshotSyncStatusKey, snapSyncStatusFlagKey,
			} {
				if bytes.Equal(key, meta) {
					metadata.Add(size)
					accounted = true
					break
				}
			}
			if !accounted {
				unaccounted.Add(size)
			}
		}
		count++
		if count%1000 == 0 && time.Since(logged) > 8*time.Second {
			log.Info("Inspecting database", "count", count, "elapsed", common.PrettyDuration(time.Since(start)))
			logged = time.Now()
		}
	}
	// inspect separate trie db
	if trieIter != nil {
		count = 0
		logged = time.Now()
		for trieIter.Next() {
			var (
				key   = trieIter.Key()
				value = trieIter.Value()
				size  = common.StorageSize(len(key) + len(value))
			)
			total += size

			switch {
			case IsLegacyTrieNode(key, value):
				legacyTries.Add(size)
			case bytes.HasPrefix(key, stateIDPrefix) && len(key) == len(stateIDPrefix)+common.HashLength:
				stateLookups.Add(size)
			case IsAccountTrieNode(key):
				accountTries.Add(size)
			case IsStorageTrieNode(key):
				storageTries.Add(size)
			default:
				var accounted bool
				for _, meta := range [][]byte{
					fastTrieProgressKey, persistentStateIDKey, trieJournalKey, snapSyncStatusFlagKey} {
					if bytes.Equal(key, meta) {
						metadata.Add(size)
						accounted = true
						break
					}
				}
				if !accounted {
					unaccounted.Add(size)
				}
			}
			count++
			if count%1000 == 0 && time.Since(logged) > 8*time.Second {
				log.Info("Inspecting separate state database", "count", count, "elapsed", common.PrettyDuration(time.Since(start)))
				logged = time.Now()
			}
		}
		log.Info("Inspecting separate state database", "count", count, "elapsed", common.PrettyDuration(time.Since(start)))
	}
	// Display the database statistic of key-value store.
	stats := [][]string{
		{"Key-Value store", "Headers", headers.Size(), headers.Count()},
		{"Key-Value store", "Bodies", bodies.Size(), bodies.Count()},
		{"Key-Value store", "Receipt lists", receipts.Size(), receipts.Count()},
		{"Key-Value store", "Difficulties", tds.Size(), tds.Count()},
		{"Key-Value store", "BlobSidecars", blobSidecars.Size(), blobSidecars.Count()},
		{"Key-Value store", "Block number->hash", numHashPairings.Size(), numHashPairings.Count()},
		{"Key-Value store", "Block hash->number", hashNumPairings.Size(), hashNumPairings.Count()},
		{"Key-Value store", "Transaction index", txLookups.Size(), txLookups.Count()},
		{"Key-Value store", "Bloombit index", bloomBits.Size(), bloomBits.Count()},
		{"Key-Value store", "Contract codes", codes.Size(), codes.Count()},
		{"Key-Value store", "Hash trie nodes", legacyTries.Size(), legacyTries.Count()},
		{"Key-Value store", "Path trie state lookups", stateLookups.Size(), stateLookups.Count()},
		{"Key-Value store", "Path trie account nodes", accountTries.Size(), accountTries.Count()},
		{"Key-Value store", "Path trie storage nodes", storageTries.Size(), storageTries.Count()},
		{"Key-Value store", "Verkle trie nodes", verkleTries.Size(), verkleTries.Count()},
		{"Key-Value store", "Verkle trie state lookups", verkleStateLookups.Size(), verkleStateLookups.Count()},
		{"Key-Value store", "Trie preimages", preimages.Size(), preimages.Count()},
		{"Key-Value store", "Account snapshot", accountSnaps.Size(), accountSnaps.Count()},
		{"Key-Value store", "Storage snapshot", storageSnaps.Size(), storageSnaps.Count()},
		{"Key-Value store", "Clique snapshots", cliqueSnaps.Size(), cliqueSnaps.Count()},
		{"Key-Value store", "Parlia snapshots", parliaSnaps.Size(), parliaSnaps.Count()},
		{"Key-Value store", "Singleton metadata", metadata.Size(), metadata.Count()},
		{"Light client", "CHT trie nodes", chtTrieNodes.Size(), chtTrieNodes.Count()},
		{"Light client", "Bloom trie nodes", bloomTrieNodes.Size(), bloomTrieNodes.Count()},
	}
	// Inspect all registered append-only file store then.
	ancients, err := inspectFreezers(db)
	if err != nil {
		return err
	}
	for _, ancient := range ancients {
		for _, table := range ancient.sizes {
			stats = append(stats, []string{
				fmt.Sprintf("Ancient store (%s)", strings.Title(ancient.name)),
				strings.Title(table.name),
				table.size.String(),
				fmt.Sprintf("%d", ancient.count()),
			})
		}
		total += ancient.size()
	}

	// inspect ancient state in separate trie db if exist
	if trieIter != nil {
		stateAncients, err := inspectFreezers(db.GetStateStore())
		if err != nil {
			return err
		}
		for _, ancient := range stateAncients {
			for _, table := range ancient.sizes {
				if ancient.name == "chain" {
					break
				}
				stats = append(stats, []string{
					fmt.Sprintf("Ancient store (%s)", strings.Title(ancient.name)),
					strings.Title(table.name),
					table.size.String(),
					fmt.Sprintf("%d", ancient.count()),
				})
			}
			total += ancient.size()
		}
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Database", "Category", "Size", "Items"})
	table.SetFooter([]string{"", "Total", total.String(), " "})
	table.AppendBulk(stats)
	table.Render()

	if unaccounted.size > 0 {
		log.Error("Database contains unaccounted data", "size", unaccounted.size, "count", unaccounted.count)
	}
	return nil
}

func DeleteTrieState(db ethdb.Database) error {
	var (
		it     ethdb.Iterator
		batch  = db.NewBatch()
		start  = time.Now()
		logged = time.Now()
		count  int64
		key    []byte
	)

	prefixKeys := map[string]func([]byte) bool{
		string(TrieNodeAccountPrefix): IsAccountTrieNode,
		string(TrieNodeStoragePrefix): IsStorageTrieNode,
		string(stateIDPrefix):         func(key []byte) bool { return len(key) == len(stateIDPrefix)+common.HashLength },
	}

	for prefix, isValid := range prefixKeys {
		it = db.NewIterator([]byte(prefix), nil)

		for it.Next() {
			key = it.Key()
			if !isValid(key) {
				continue
			}

			batch.Delete(it.Key())
			if batch.ValueSize() > ethdb.IdealBatchSize {
				if err := batch.Write(); err != nil {
					it.Release()
					return err
				}
				batch.Reset()
			}

			count++
			if time.Since(logged) > 8*time.Second {
				log.Info("Deleting trie state", "count", count, "elapsed", common.PrettyDuration(time.Since(start)))
				logged = time.Now()
			}
		}

		it.Release()
	}

	if batch.ValueSize() > 0 {
		if err := batch.Write(); err != nil {
			return err
		}
		batch.Reset()
	}

	log.Info("Deleted trie state", "count", count, "elapsed", common.PrettyDuration(time.Since(start)))

	return nil
}

// printChainMetadata prints out chain metadata to stderr.
func printChainMetadata(db ethdb.Reader) {
	fmt.Fprintf(os.Stderr, "Chain metadata\n")
	for _, v := range ReadChainMetadata(db) {
		fmt.Fprintf(os.Stderr, "  %s\n", strings.Join(v, ": "))
	}
	fmt.Fprintf(os.Stderr, "\n\n")
}

// ReadChainMetadata returns a set of key/value pairs that contains information
// about the database chain status. This can be used for diagnostic purposes
// when investigating the state of the node.
func ReadChainMetadata(db ethdb.Reader) [][]string {
	pp := func(val *uint64) string {
		if val == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%d (%#x)", *val, *val)
	}
	data := [][]string{
		{"databaseVersion", pp(ReadDatabaseVersion(db))},
		{"headBlockHash", fmt.Sprintf("%v", ReadHeadBlockHash(db))},
		{"headFastBlockHash", fmt.Sprintf("%v", ReadHeadFastBlockHash(db))},
		{"headHeaderHash", fmt.Sprintf("%v", ReadHeadHeaderHash(db))},
		{"lastPivotNumber", pp(ReadLastPivotNumber(db))},
		{"len(snapshotSyncStatus)", fmt.Sprintf("%d bytes", len(ReadSnapshotSyncStatus(db)))},
		{"snapshotDisabled", fmt.Sprintf("%v", ReadSnapshotDisabled(db))},
		{"snapshotJournal", fmt.Sprintf("%d bytes", len(ReadSnapshotJournal(db)))},
		{"snapshotRecoveryNumber", pp(ReadSnapshotRecoveryNumber(db))},
		{"snapshotRoot", fmt.Sprintf("%v", ReadSnapshotRoot(db))},
		{"txIndexTail", pp(ReadTxIndexTail(db))},
	}
	return data
}
