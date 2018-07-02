package macaroons

import (
	"fmt"
	"time"
	"crypto/rand"
	"encoding/binary"
	
	"github.com/coreos/bbolt"
	"github.com/lightningnetwork/lnd/lnwire"
)

// AccountType is an enum-like type which denotes the possible account types
// that can be referenced in macaroons to keep track of user's balances.
type AccountType uint8

// AccountID is the type of an account's unique ID.
type AccountIDType [AccountIDLen]byte

const (
	// AccountIDLen is the length of the ID that is generated as an
	// unique identifier of an account. It is 16 bytes long so guessing
	// is improbable but it's still not mistaken for a SHA256 hash.
	AccountIDLen = 16

	// OneTimeBalance represents an account that has an initial balance
	// that is used up when it is spent and is not replenished
	// automatically.
	OneTimeBalance AccountType = iota

	// PeriodicBalance represents an account that gets its balance
	// replenished after a certain amount of time has passed.
	PeriodicBalance 
)

var (
	// accountBucketName is the name of the bucket where all accounting
	// based balances are stored.
	accountBucketName = []byte("accounts")

	// ErrMalformed is returned if a binary stored account cannot be
	// marshaled back.
	ErrMalformed = fmt.Errorf("malformed data")
	
	// ErrAccNotFound is returned if an account could not be found in the
	// local bolt DB.
	ErrAccNotFound = fmt.Errorf("account not found")
)

// OffChainBalanceAccount holds all information that is needed to keep track
// of an user's off-chain account balance. This balance can only be spent
// by paying invoices.
type OffChainBalanceAccount struct {
	// ID is the randomly generated account identifier.
	ID AccountIDType

	// Type is the account type.
	Type AccountType

	// InitialBalance stores the initial balance and is never updated.
	InitialBalance lnwire.MilliSatoshi

	// CurrentBalance is the currently available balance of the account
	// that is updated every time an invoice is paid.
	CurrentBalance lnwire.MilliSatoshi

	// LastUpdate keeps track of the last time the balance of the account
	// was updated.
	LastUpdate time.Time

	// ExpirationDate is a specific date in the future after which the
	// account is marked as expired. Can be set to nil for accounts that
	// never expire.
	ExpirationDate time.Time
}

// Marshal returns the account marshaled into a format suitable for storage.
func (a *OffChainBalanceAccount) Marshal() ([]byte, error) {
	// The marshaled format for the the account is as follows:
	//   <ID><Type><InitialBalance><CurrentBalance><LastUpdate>
	//   <ExpirationDate>
	//
	// The time.Time type is 15 bytes long when binary marshaled.
	//
	// AccountIdLen + Type (1 byte) + InitialBalance (8 bytes) +
	// CurrentBalance (8 bytes) + LastUpdate (15 bytes) +
	// ExpirationDate (15 bytes) = AccountIdLen + 47 bytes = 63 bytes
	marshaled := make([]byte, AccountIDLen+47)

	b := marshaled
	copy(b[:AccountIDLen], a.ID[:])
	b = b[AccountIDLen:]
	b[0] = byte(a.Type)
	b = b[1:]
	binary.LittleEndian.PutUint64(b[:8], uint64(a.InitialBalance))
	b = b[8:]
	binary.LittleEndian.PutUint64(b[:8], uint64(a.CurrentBalance))
	b = b[8:]
	lastUpdateMarshaled, err := a.LastUpdate.MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy(b[:15], lastUpdateMarshaled)
	b = b[15:]
	expirationDateMarshaled, err := a.ExpirationDate.MarshalBinary()
	if err != nil {
		return nil, err
	}
	copy(b[:15], expirationDateMarshaled)

	return marshaled, nil
}

// Unmarshal unmarshals the account from a binary format.
func (a *OffChainBalanceAccount) Unmarshal(marshaled []byte) error {
	// The marshaled format for the the account is as follows:
	//   <ID><Type><InitialBalance><CurrentBalance><LastUpdate>
	//   <ExpirationDate>
	//
	// The time.Time type is 15 bytes long when binary marshaled.
	//
	// AccountIdLen + Type (1 byte) + InitialBalance (8 bytes) +
	// CurrentBalance (8 bytes) + LastUpdate (15 bytes) +
	// ExpirationDate (15 bytes) = AccountIdLen + 47 bytes = 63 bytes
	if len(marshaled) != AccountIDLen+47 {
		return ErrMalformed
	}

	copy(a.ID[:], marshaled[:AccountIDLen])
	marshaled = marshaled[AccountIDLen:]
	a.Type = AccountType(marshaled[0])
	marshaled = marshaled[1:]
	a.InitialBalance = lnwire.MilliSatoshi(
		binary.LittleEndian.Uint64(marshaled[:8]),
	)
	marshaled = marshaled[8:]
	a.CurrentBalance = lnwire.MilliSatoshi(
		binary.LittleEndian.Uint64(marshaled[:8]),
	)
	marshaled = marshaled[8:]
	a.LastUpdate = time.Time{}
	if err := a.LastUpdate.UnmarshalBinary(marshaled[:15]); err != nil {
		return err
	}
	marshaled = marshaled[15:]
	a.ExpirationDate = time.Time{}
	if err := a.ExpirationDate.UnmarshalBinary(marshaled[:15]); err != nil {
		return err
	}

	return nil
}

// AccountStorage wraps the bolt DB that stores all accounts and their balances.
type AccountStorage struct {
	*bolt.DB
}

// NewAccountStorage creates an AccountStorage instance and the corresponding
// bucket in the bolt DB if it does not exist yet.
func NewAccountStorage(db *bolt.DB) (*AccountStorage, error) {
	// If the store's bucket doesn't exist, create it.
	err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(accountBucketName)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Return the DB wrapped in a AccountStorage object.
	return &AccountStorage{db}, nil
}

// NewAccount creates a new OffChainBalanceAccount with the given balance and a
// randomly chosen ID.
func (s *AccountStorage) NewAccount(balance lnwire.MilliSatoshi,
	expirationDate time.Time) (*OffChainBalanceAccount, error) {
	// First, create a new instance of an account. Currently only the type
	// OneTimeBalance is supported.
	account := &OffChainBalanceAccount{
		Type:           OneTimeBalance,
		InitialBalance: balance,
		CurrentBalance: balance,
		LastUpdate:     time.Now(),
		ExpirationDate: expirationDate,
	}
	if _, err := rand.Read(account.ID[:]); err != nil {
		return nil, err
	}

	// Try storing the account in the account database so we can keep track
	// of its balance.
	err := s.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(accountBucketName)
		accountBinary, err := account.Marshal()
		if err != nil {
			return err
		}
		err = bucket.Put(account.ID[:], accountBinary)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// GetAccount retrieves an account from the bolt DB and unmarshals it. If the
// account cannot be found, then ErrAccNotFound is returned.
func (s *AccountStorage) GetAccount(
	id AccountIDType) (*OffChainBalanceAccount, error) {
	// Try looking up and reading the account by its ID from the local
	// bolt DB.
	var accountBinary []byte
	err := s.View(func(tx *bolt.Tx) error {
		accountBinary = tx.Bucket(accountBucketName).Get(id[:])
		if len(accountBinary) == 0 {
			return ErrAccNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	
	// Now try unmarshaling the account back from the binary format it was
	// stored in.
	account := &OffChainBalanceAccount{}
	if err := account.Unmarshal(accountBinary); err != nil {
		return nil, err
	}

	return account, nil
}

// GetAccounts retrieves all accounts from the bolt DB and unmarshals them.
func (s *AccountStorage) GetAccounts() ([]*OffChainBalanceAccount, error) {
	var accounts []*OffChainBalanceAccount
	err := s.View(func(tx *bolt.Tx) error {
		// This function will be called in the ForEach and receive
		// the key and value of each account in the DB. The key, which
		// is also the ID is not used because it is also marshaled into
		// the value.
		readFn := func(_,v []byte) error {
			if v == nil {
				return nil
			}
			account := &OffChainBalanceAccount{}
			if err := account.Unmarshal(v); err != nil {
				return err
			}
			accounts = append(accounts, account)

			return nil
		}
		
		// We know the bucket should exist since it's created when
		// the account storage is initialized.
		return tx.Bucket(accountBucketName).ForEach(readFn)

	})
	if err != nil {
		return nil, err
	}
	
	return accounts, nil
}

// Close closes the underlying database.
func (s *AccountStorage) Close() error {
	return s.DB.Close()
}
