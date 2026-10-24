package app

import (
	"errors"
	"strconv"

	"github.com/egoist/mygo"
)

func historyCheckpointKey(email string) string {
	return "gmail-history-" + email
}

func loadHistoryCheckpoint(email string) (uint64, bool, error) {
	value, err := mygo.Secrets.Get(historyCheckpointKey(email))
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	id, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func saveHistoryCheckpoint(email string, id uint64) error {
	return mygo.Secrets.Set(historyCheckpointKey(email), []byte(strconv.FormatUint(id, 10)))
}

func deleteHistoryCheckpoint(email string) error {
	return mygo.Secrets.Delete(historyCheckpointKey(email))
}
