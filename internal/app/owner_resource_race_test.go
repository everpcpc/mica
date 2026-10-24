package app

import (
	"sync"
	"testing"

	"mica/internal/mail"
)

func TestResourceValidationConcurrentWithRegistryReplacement(t *testing.T) {
	a := New(newFakeService())
	account := mail.Account{Email: "b@example.com"}
	a.registerAccountClient(newFakeService(), account, nil, true)
	resource := emailResource{binding: a.emailResources.bindingFor(mail.Message{AccountEmail: account.Email, ID: "message", ThreadID: "thread"})}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			a.emailResources.valid(resource)
		}
	}()
	for range 1000 {
		a.registerAccountClient(newFakeService(), account, nil, true)
		a.removeAccountClient(account.Email)
	}
	wg.Wait()
	if a.emailResources.valid(resource) {
		t.Fatal("removed account retained a valid resource")
	}
	a.registerAccountClient(newFakeService(), account, nil, true)
	replacement := emailResource{binding: a.emailResources.bindingFor(mail.Message{AccountEmail: account.Email, ID: "message", ThreadID: "thread"})}
	if !a.emailResources.valid(replacement) {
		t.Fatal("replacement account resource was not accepted")
	}
}

func TestActiveResourceValidationUsesOwnerGeneration(t *testing.T) {
	a := New(newFakeService())
	a.storeAccount = "a@example.com"
	a.nextAccountGeneration(a.storeAccount)
	resource := emailResource{binding: a.emailResources.bindingFor(mail.Message{AccountEmail: a.storeAccount, ID: "message", ThreadID: "thread"})}
	if !a.emailResources.valid(resource) {
		t.Fatal("active resource was rejected with mismatched generation")
	}
	a.nextAccountGeneration(a.storeAccount)
	if a.emailResources.valid(resource) {
		t.Fatal("stale active resource remained valid")
	}
}
