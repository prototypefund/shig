package lobby

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func testRtpStreamLobbyRepositorySetup(t *testing.T) *RtpStreamLobbyRepository {
	t.Helper()
	var engine rtpEngine
	repository := newRtpStreamLobbyRepository(engine)

	return repository
}
func TestStreamLobbyRepository(t *testing.T) {

	t.Run("Get not existing Lobby", func(t *testing.T) {
		repo := testRtpStreamLobbyRepositorySetup(t)
		space, ok := repo.getLobby(uuid.New())
		assert.False(t, ok)
		assert.Nil(t, space)
	})

	t.Run("Create Lobby", func(t *testing.T) {
		repo := testRtpStreamLobbyRepositorySetup(t)
		lobby := repo.getOrCreateLobby(uuid.New())
		assert.NotNil(t, lobby)
	})

	t.Run("Create and Get Lobby", func(t *testing.T) {
		repo := testRtpStreamLobbyRepositorySetup(t)
		id := uuid.New()
		lobbyCreated := repo.getOrCreateLobby(id)
		assert.NotNil(t, lobbyCreated)
		lobbyGet, ok := repo.getLobby(id)
		assert.True(t, ok)
		assert.Same(t, lobbyCreated, lobbyGet)
	})

	t.Run("Delete Lobby", func(t *testing.T) {
		repo := testRtpStreamLobbyRepositorySetup(t)
		id := uuid.New()
		created := repo.getOrCreateLobby(id)
		assert.NotNil(t, created)

		deleted := repo.Delete(id)
		assert.True(t, deleted)

		get, ok := repo.getLobby(id)
		assert.False(t, ok)
		assert.Nil(t, get)
	})

	t.Run("Safely Concurrently Adding and Deleting", func(t *testing.T) {
		wantedCount := 1000
		createOn := 200
		deleteOn := 500
		id := uuid.New()
		repo := testRtpStreamLobbyRepositorySetup(t)

		var wg sync.WaitGroup
		wg.Add(wantedCount + 2)
		created := make(chan struct{})

		for i := 0; i < wantedCount; i++ {
			go func(id int) {
				lobby := repo.getOrCreateLobby(uuid.New())
				assert.NotNil(t, lobby)
				wg.Done()
			}(i)

			if i == createOn {
				go func() {
					lobby := repo.getOrCreateLobby(id)
					assert.NotNil(t, lobby)
					close(created)
					wg.Done()
				}()
			}

			if i == deleteOn {
				go func() {
					<-created
					deleted := repo.Delete(id)
					assert.True(t, deleted)
					wg.Done()
				}()
			}
		}

		wg.Wait()

		_, ok := repo.getLobby(id)
		assert.False(t, ok)
		assert.Equal(t, wantedCount, repo.Len())
	})
}