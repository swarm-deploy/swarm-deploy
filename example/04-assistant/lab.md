# Цель

Создать ассистента, который будет помогать девопсу/разработчику с деплоем приложений в Docker Swarm, а также дебагом.

Асисстент внедрен прямо в GitOps платформу `swarm-deploy`. swarm-deploy - это аналог ArgoCD, только для Docker Swarm.

Проект написан на Go с использованием библиотек:
- LangGraph - `github.com/tmc/langgraphgo`
- LangChain - `github.com/tmc/langchaingo`
- OpenAI-Go - `github.com/openai/openai-go`

# Чат 

Чат с ассистентом находится в UI проекта. Открывается по нажатию на кнопку "UI".

API эндпоинт `/api/v1/assistant/chat`, как и весь API и UI закрыт basic-аутентификацией.

Веб-сокеты не поднимал, используется Long Polling.

# Модель

В среде эксплуатации использую модель `Qwen/Qwen3-Coder-Next` от Cloud.Ru Evolution Foundation Models.
Для эмбеддингов использую модель `Qwen/Qwen3-Embedding-0.6B`.

Также в конфигурации проекта можно указать температуру и максимальное количество токенов.

Полная конфигурация для запуска ассистента:

```yaml
# AI assistant settings.
assistant:
  # Enable assistant API and UI.
  enabled: true
  # Allowed tool list. Empty means all built-in tools.
  tools: []
  # Extra project-specific prompt.
  systemPrompt: ""
  model:
    # Chat model name.
    name: Qwen/Qwen3-Coder-Next
    embeddingName: "Qwen/Qwen3-Embedding-0.6B"
    openai:
      # OpenAI-compatible API base URL.
      baseUrl: https://foundation-models.api.cloud.ru/v1
      # Path to file containing API token.
      apiTokenPath: /run/secrets/swarm-deploy-openai-api-token
      # Optional OpenAI organization identifier.
      organizationId: "<ID проекта в Cloud.ru>"
      # Sampling temperature in range [0, 2].
      temperature: "0.2"
      # Max generated tokens for response.
      maxTokens: "800"
```

# Системный промпт

Системный промпт содержит:
- Описание роли ассистента
- Базовую информацию о проекте
- Политики вызова MCP
- Защита от Prompt Injection

Промпт находится в: [internal/assistant/prompt.md](./../../internal/assistant/prompt.md)
При сборке образа, промпт грузится в память приложения. Промпт также можно дополнить правилами конкретного проекта/окружения.

Также реализована защита от иньъекций регулярками: [internal/assistant/guard/injection.go](./../../internal/assistant/guard/injection.go)

# RAG

Реализован RAG по сервисам, задеплоенным в Docker Swarm, включая: название, образ, тип сервиса (бизнес-приложение или инфраструктура), веб-маршруты.

- Переменные окружения и секреты в RAG не отправляются. Мы не можем полагаться на то, что в переменных окружения нет кредов - можно было бы решить вопрос маскированием кредов, но нет гарантий, что все кейсы будут покрыты.
- Веб-маршруты достаются из переменных окружения в коде.

Работа с RAG находится в: [internal/assistant/rag](./../../internal/assistant/rag)

RAG строится после деплоя сервисов.

# MCP

На данный момент для MCP инструментов не поднимается отдельный сервер, происходит прямой вызов. Считаю это оправданным для агента, внедренного прямо в приложение.

В дальнейшем думаю, что можно рассмотреть поднятие сервера на отдельном порту для того, чтобы использовать инструменты в привычном агенте. Но сейчас в проекте есть только basic-аутентификация, которой явно недостаточно для поднятия сервера.

- Просмотр истории событий. Асисстент может посмотреть, когда был последний деплой, каким было состояние приложения и какие изменения
- Просмотр списка узлов в кластере. Не стал делать RAG, потому что всегда нужно видеть актуальное состояние ноды
- Пинг веб-маршрутов сервиса. Инструмент просто отправляет запросы на урлы, по которым отвечает контейнер за reverse proxy.
- Сообщения о попытке Prompt Injection. В системном промпте модели определено поведение для определения инъекции, и дана установка вызывать MCP.
- Выполнение деплоев. В этом инструменте деплой - это по сути `git pull` + `docker stack deploy`
- Получение списка коммитов
- Получение диффа по коммиту. Дифф парсится в коде и модель не получает чувствительных данных типа значений секретов или переменных.
- Получение текущего времени и дня недели. Иначе модель начинает путаться и вычислять дату по косвенным признакам: дата последнего события или дата последнего коммита. А также модель `Qwen3` неверно определяет день недели. Важно знать текущую дату для того, чтобы корректно обрабатывать запросы по типу "Какие коммиты были вчера?"

Инструменты находятся в: [internal/entrypoints/mcpserver](./../../internal/entrypoints/mcpserver)

# Аудит

Приложение хранит историю событий, в том числе события о попытках внедрения Prompt Injection.
Prompt Injection может определить как регулярка, так и сама модель.

Например, после такого чата

![prompt_injection_chat.png](prompt_injection_chat.png)

В истории событий будут записи с:
- `Detector` - кто определил инъекцию: регулярка или модель
- `Prompt` - какой запрос поступил к ассистенту
- `Username` - аутентифицированный пользователь, который попытался атаковать ассистента

![prompt_injection_event_history.png](prompt_injection_event_history.png)

Также можно настроить алертинг в Telegram, добавив конфиг:
```yaml
notifications:
  on:
    assistantPromptInjectionDetected:
      telegram:
        - name: ops-prompt-injection
          # Path to file with bot token.
          botTokenPath: /run/secrets/swarm-deploy-notifications-telegram-token
          # Chat/channel ID.
          chatId: "<chat-id>"
          # Thread (topic) ID inside chat.
          chatThreadId: 155
          # Message text template.
          message: |
            ⚠️ Обнаружена попытка Prompt Injection.
            Промпт: {{ .event.Prompt }}
            Детектор: {{ .event.Detector }}
```

И получать вот такие сообщения:

![prompt_injection_alert.png](prompt_injection_alert.png)

# Мониторинг

Приложение отдает метрики в формате Prometheus.

Метрики, релевантные агенту:
- MCP: Количество вызовов инструментов и результат выполнения
- MCP: Время выполнения инструментов
- MCP: Количество вызовов неизвестных инструментов - пока что мне не удалось поймать такой кейс, но думаю, что модель ошибиться и вызвать незарегистрированную тулу
- RAG: Время сборки документов
- RAG: Количество фоллбэков на обработку запроса в коде
- RAG: Размер индекса
- RAG: Время последнего обновления индекса

Также в проекте есть общие метрики на количество событий. Здесь смотрим на события Prompt Injection.

- Метрики для MCP: [internal/metrics/mcp.go](../../internal/metrics/mcp.go)
- Метрики для RAG: [internal/metrics/assistant.go](../../internal/metrics/assistant.go)
- Конфигурации для Prometheus/Grafana: [monitoring](./../../monitoring)

Пример реального дашборда

![](./../../monitoring/example_1.png)
![](./../../monitoring/example_2.png)
