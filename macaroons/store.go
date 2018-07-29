package macaroons

import (
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/net/context"

	"github.com/coreos/bbolt"

	"github.com/btcsuite/btcwallet/snacl"
)

const (
	// RootKeyLen is the length of a root key.
	RootKeyLen = 32
)

var (
	// rootKeyBucketName is the name of the root key store bucket.
	rootKeyBucketName = []byte("macrootkeys")

	// defaultRootKeyID is the ID of the default root key. The first is
	// just 0, to emulate the memory storage that comes with bakery.
	//
	// TODO(aakselrod): Add support for key rotation.
	defaultRootKeyID = []byte("0")

	// encryptionKeyID is the name of the database key that stores the
	// encryption key, encrypted with a salted + hashed password. The
	// format is 32 bytes of salt, and the rest is encrypted key.
	encryptionKeyID = []byte("enckey")

	// ErrAlreadyUnlocked specifies that the store has already been
	// unlocked.
	ErrAlreadyUnlocked = fmt.Errorf("macaroon store already unlocked")

	// ErrStoreLocked specifies that the store needs to be unlocked with
	// a password.
	ErrStoreLocked = fmt.Errorf("macaroon store is locked")

	// ErrPasswordRequired specifies that a nil password has been passed.
	ErrPasswordRequired = fmt.Errorf("a non-nil password is required")

	// ErrEncKeyNotFound specifies that there was no encryption key found
	// even if one was expected to be generated.
	ErrEncKeyNotFound = fmt.Errorf("macaroon encryption key not found")
)

// RootKeyStorage implements the bakery.RootKeyStorage interface.
type RootKeyStorage struct {
	*bolt.DB

	encKey *snacl.SecretKey
}

// NewRootKeyStorage creates a RootKeyStorage instance.
// TODO(aakselrod): Add support for encryption of data with passphrase.
func NewRootKeyStorage(db *bolt.DB) (*RootKeyStorage, error) {
	// If the store's bucket doesn't exist, create it.
	err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(rootKeyBucketName)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Return the DB wrapped in a RootKeyStorage object.
	return &RootKeyStorage{db, nil}, nil
}

// CreateUnlock sets an encryption key if one is not already set, otherwise it
// checks if the password is correct for the stored encryption key.
func (r *RootKeyStorage) CreateUnlock(password *[]byte) error {
	// Check if we've already unlocked the store; return an error if so.
	if r.encKey != nil {
		return ErrAlreadyUnlocked
	}

	// Check if a nil password has been passed; return an error if so.
	if password == nil {
		return ErrPasswordRequired
	}

	return r.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(rootKeyBucketName)
		dbKey := bucket.Get(encryptionKeyID)
		if len(dbKey) > 0 {
			// We've already stored a key, so try to unlock with
			// the password.
			encKey := &snacl.SecretKey{}
			err := encKey.Unmarshal(dbKey)
			if err != nil {
				return err
			}

			err = encKey.DeriveKey(password)
			if err != nil {
				return err
			}

			r.encKey = encKey
			return nil
		}

		// We haven't yet stored a key, so create a new one.
		encKey, err := snacl.NewSecretKey(password, snacl.DefaultN,
			snacl.DefaultR, snacl.DefaultP)
		if err != nil {
			return err
		}

		err = bucket.Put(encryptionKeyID, encKey.Marshal())
		if err != nil {
			return err
		}

		r.encKey = encKey
		return nil
	})
}

// ChangePassword decrypts the macaroon root key with the old password and then
// encrypts it again with the new password.
func (r *RootKeyStorage) ChangePassword(oldPassword *[]byte,
	newPassword *[]byte) error {
	// We need the store to already be unlocked. With this we can make sure
	// that there already is a key in the DB.
	if r.encKey == nil {
		return ErrStoreLocked
	}

	// Check if a nil password has been passed; return an error if so.
	if oldPassword == nil || newPassword == nil {
		return ErrPasswordRequired
	}

	return r.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(rootKeyBucketName)
		encKeyDb := bucket.Get(encryptionKeyID)
		rootKeyDb := bucket.Get(defaultRootKeyID)

		// Both the encryption key and the root key mus be present
		// otherwise we are in the wrong state to change the password.
		if len(encKeyDb) == 0 || len(rootKeyDb) == 0 {
			return ErrEncKeyNotFound
		}

		// Unmarshal parameters for old encryption key and derive the
		// old key with them.
		encKeyOld := &snacl.SecretKey{}
		err := encKeyOld.Unmarshal(encKeyDb)
		if err != nil {
			return err
		}
		err = encKeyOld.DeriveKey(oldPassword)
		if err != nil {
			return err
		}

		// Create a new encryption key from the new password.
		encKeyNew, err := snacl.NewSecretKey(
			newPassword, snacl.DefaultN, snacl.DefaultR,
			snacl.DefaultP,
		)
		if err != nil {
			return err
		}

		// Now try to decrypt the root key with the old encryption key,
		// encrypt it with the new one and then store it in the DB.
		decryptedKey, err := encKeyOld.Decrypt(rootKeyDb)
		if err != nil {
			return err
		}
		rootKey := make([]byte, len(decryptedKey))
		copy(rootKey[:], decryptedKey[:])
		encryptedKey, err := encKeyNew.Encrypt(rootKey)
		if err != nil {
			return err
		}
		err = bucket.Put(defaultRootKeyID, encryptedKey)
		if err != nil {
			return err
		}

		// Finally, store the new encryption key parameters in the DB
		// as well.
		err = bucket.Put(encryptionKeyID, encKeyNew.Marshal())
		if err != nil {
			return err
		}

		r.encKey = encKeyNew
		return nil
	})
}

// Get implements the Get method for the bakery.RootKeyStorage interface.
func (r *RootKeyStorage) Get(_ context.Context, id []byte) ([]byte, error) {
	if r.encKey == nil {
		return nil, ErrStoreLocked
	}
	var rootKey []byte
	err := r.View(func(tx *bolt.Tx) error {
		dbKey := tx.Bucket(rootKeyBucketName).Get(id)
		if len(dbKey) == 0 {
			return fmt.Errorf("root key with id %s doesn't exist",
				string(id))
		}

		decKey, err := r.encKey.Decrypt(dbKey)
		if err != nil {
			return err
		}

		rootKey = make([]byte, len(decKey))
		copy(rootKey[:], decKey)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return rootKey, nil
}

// RootKey implements the RootKey method for the bakery.RootKeyStorage
// interface.
// TODO(aakselrod): Add support for key rotation.
func (r *RootKeyStorage) RootKey(_ context.Context) ([]byte, []byte, error) {
	if r.encKey == nil {
		return nil, nil, ErrStoreLocked
	}
	var rootKey []byte
	id := defaultRootKeyID
	err := r.Update(func(tx *bolt.Tx) error {
		ns := tx.Bucket(rootKeyBucketName)
		dbKey := ns.Get(id)

		// If there's a root key stored in the bucket, decrypt it and
		// return it.
		if len(dbKey) != 0 {
			decKey, err := r.encKey.Decrypt(dbKey)
			if err != nil {
				return err
			}

			rootKey = make([]byte, len(decKey))
			copy(rootKey[:], decKey[:])
			return nil
		}

		// Otherwise, create a RootKeyLen-byte root key, encrypt it,
		// and store it in the bucket.
		rootKey = make([]byte, RootKeyLen)
		if _, err := io.ReadFull(rand.Reader, rootKey[:]); err != nil {
			return err
		}

		encKey, err := r.encKey.Encrypt(rootKey)
		if err != nil {
			return err
		}
		return ns.Put(id, encKey)
	})
	if err != nil {
		return nil, nil, err
	}

	return rootKey, id, nil
}

// GenerateNewRootKey generates a new macaroon root key, replacing the previous
// root key if it existed.
func (r *RootKeyStorage) GenerateNewRootKey() error {
	// We need the store to already be unlocked. With this we can make sure
	// that there already is a key in the DB that can be replaced.
	if r.encKey == nil {
		return ErrStoreLocked
	}
	err := r.Update(func(tx *bolt.Tx) error {
		// Create a new RootKeyLen-byte root key, encrypt it,
		// and store it in the bucket. Any previously set key will
		// be overwritten.
		bucket := tx.Bucket(rootKeyBucketName)
		rootKey := make([]byte, RootKeyLen)
		if _, err := io.ReadFull(rand.Reader, rootKey[:]); err != nil {
			return err
		}

		encryptedKey, err := r.encKey.Encrypt(rootKey)
		if err != nil {
			return err
		}
		return bucket.Put(defaultRootKeyID, encryptedKey)
	})
	if err != nil {
		return err
	}

	return nil
}

// Close closes the underlying database and zeroes the encryption key stored
// in memory.
func (r *RootKeyStorage) Close() error {
	if r.encKey != nil {
		r.encKey.Zero()
	}
	return r.DB.Close()
}
