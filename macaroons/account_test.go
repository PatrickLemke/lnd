package macaroons_test

import (
	"testing"
	"os"
	"time"
	"bytes"
	
	"github.com/lightningnetwork/lnd/macaroons"
)

var (
	testExpDate = time.Date(2018, time.July, 2, 15, 44, 0, 0, time.UTC)
)

// TestNewAccount tests the creation of a new account and its marshaling
// and unmarshaling into the bolt DB.
func TestNewAccount(t *testing.T) {
	// First, initialize a dummy DB file with a store that the service
	// can read from. Make sure the file is removed in the end.
	tempDir := setupTestRootKeyStorage(t)
	defer os.RemoveAll(tempDir)

	// Second, create the new service instance, unlock it and pass in a
	// checker that we expect it to add to the bakery.
	service, err := macaroons.NewService(tempDir)
	defer service.Close()
	if err != nil {
		t.Fatalf("Error creating new service: %v", err)
	}
	err = service.CreateUnlock(&defaultPw)
	if err != nil {
		t.Fatalf("Error unlocking root key storage: %v", err)
	}

	// Now let's create a new account. It is automatically marshaled
	// and stored in the account bolt DB.
	account, err := service.NewAccount(9735, testExpDate)
	if err != nil {
		t.Fatalf("Error creating account: %v", err)
	}

	// Fetch the same account from the bolt DB again and compare
	// it to the initial version.
	fetchedAccount, err := service.GetAccount(account.ID)
	if err != nil {
		t.Fatalf("Error fetching account: %v", err)
	}
	if !bytes.Equal(account.ID[:], fetchedAccount.ID[:]) {
		t.Fatalf(
			"Mismatched IDs. Expected %s, got %s.", account.ID,
			fetchedAccount.ID,
		)
	}
	if account.Type != fetchedAccount.Type {
		t.Fatalf(
			"Mismatched types. Expected %d, got %d.", account.Type,
			fetchedAccount.Type,
		)
	}
	if account.InitialBalance != fetchedAccount.InitialBalance {
		t.Fatalf(
			"Mismatched initial balances. Expected %d, got %d.",
			account.InitialBalance, fetchedAccount.InitialBalance,
		)
	}
	if account.CurrentBalance != fetchedAccount.CurrentBalance {
		t.Fatalf(
			"Mismatched current balances. Expected %d, got %d.",
			account.CurrentBalance, fetchedAccount.CurrentBalance,
		)
	}
	if !account.LastUpdate.Equal(fetchedAccount.LastUpdate) {
		t.Fatalf(
			"Mismatched last update. Expected %d, got %d.",
			account.LastUpdate.Unix(),
			fetchedAccount.LastUpdate.Unix(),
		)
	}
	if !account.ExpirationDate.Equal(fetchedAccount.ExpirationDate) {
		t.Fatalf(
			"Mismatched expiration date. Expected %d, got %d.",
			account.ExpirationDate.Unix(),
			fetchedAccount.ExpirationDate.Unix(),
		)
	}
	
	// Finally, fetch all accounts and see if we also get the same account.
	accounts, err := service.GetAccounts()
	if err != nil {
		t.Fatalf("Error fetching accounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf(
			"Mismatched number of accounts. Expected %d, got %d.",
			1, len(accounts),
		)
	}
	if !bytes.Equal(accounts[0].ID[:], account.ID[:]) {
		t.Fatalf(
			"Mismatched IDs. Expected %s, got %s.", account.ID,
			fetchedAccount.ID,
		)
	}
}
