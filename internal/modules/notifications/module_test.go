package notifications

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestTelegramChannelIDDistinguishesDestinationsAndHidesToken(t *testing.T) {
	base := config.TelegramChannel{Name: "ops", ChatID: "-1", ChatThreadID: 1}
	base.BotToken.Content = []byte("secret-token")

	sameNameOtherChat := base
	sameNameOtherChat.ChatID = "-2"
	otherThread := base
	otherThread.ChatThreadID = 2
	otherBot := base
	otherBot.BotToken.Content = []byte("other-token")

	id := telegramChannelID(base)
	assert.Equal(t, id, telegramChannelID(base), "stable")
	assert.NotEqual(t, id, telegramChannelID(sameNameOtherChat), "chat")
	assert.NotEqual(t, id, telegramChannelID(otherThread), "thread")
	assert.NotEqual(t, id, telegramChannelID(otherBot), "bot")
	assert.NotContains(t, id, "secret-token", "token is not exposed")
}
