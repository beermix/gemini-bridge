# Архитектурная спецификация (Design Doc): Сервис Gemini-Bridge для VPS

## 1. Введение и цели

**Gemini-Bridge** — легковесный высокопроизводительный сервис-прокси на языке Go, разработанный для Linux VPS.
Сервис вдохновлен архитектурой модуля `proxy-gateway` десктопного приложения **AntigravityManager** и предназначен для прозрачной работы автономных AI-агентов (в первую очередь **hermes-agent**, а также OpenClaw, Cline, Roo Code и любых клиентов OpenAI / Anthropic SDK).

### Ключевые отличия от agy-bridge v1:
- **Прямое взаимодействие по HTTP/HTTPS**: сервису больше не требуется установленный в системе Node.js, локальный CLI `agy` или запуск дочерних процессов.
- **Взаимодействие с Google Cloud Code API**: запросы преобразуются в нативный формат `https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse` и `:generateContent` с использованием OAuth access token и project ID из экспорта аккаунтов.
- **Автоматический жизненный цикл токенов**: сервис самостоятельно отслеживает срок действия токенов, выполняет рефреш через `https://oauth2.googleapis.com/token`, находит `project_id` через `loadCodeAssist` и сохраняет свежие токены в кэш на диске.
- **Отказоустойчивый пул (Account Pool)**: автоматический Round-Robin между аккаунтами, обнаружение HTTP 429 (лимиты квот) с установкой Cooldown и мгновенным повтором запроса через следующий рабочий аккаунт.
- **Интерактивный TUI**: встроенный терминальный интерфейс на базе **Bubbletea + Lipgloss**, позволяющий мониторить статус аккаунтов, просматривать активность в реальном времени и закреплять (Pin) конкретный аккаунт на лету.
- **Архитектура Server + TUI Client**: фоновый сервис работает круглосуточно (например, через `systemd`), а интерактивный TUI можно в любой момент запустить по SSH, подключиться к локальному Admin IPC сокету и безопасно отключиться (`detach`) без прерывания прокси.

---

## 2. Архитектура и структура проекта

```plaintext
gemini-bridge/
├── cmd/
│   └── gemini-bridge/
│       └── main.go                 # Роутинг команд: gemini-bridge [serve|tui], флаги
├── internal/
│   ├── account/
│   │   ├── model.go                # Модели CloudAccount, TokenData, QuotaData, ExportEnvelope
│   │   ├── loader.go               # Сканирование и загрузка cloud-accounts-export-*.json
│   │   ├── oauth.go                # Обновление OAuth токенов через Google OAuth API
│   │   ├── project.go              # Определение project_id через loadCodeAssist
│   │   └── pool.go                 # Менеджер пула (Round-Robin, Pin, Cooldown при 429)
│   ├── google/
│   │   ├── client.go               # HTTP-клиент к cloudcode-pa.googleapis.com/v1internal
│   │   ├── transport.go            # Поддержка HTTP/SOCKS5 прокси (per-account или глобальный)
│   │   └── types.go                # Модели запросов/ответов Gemini internal (GeminiRequest, Part)
│   ├── proxy/
│   │   ├── server.go               # HTTP сервер прокси (порт 8045)
│   │   ├── auth.go                 # Проверка API-ключа клиента (hermes-agent)
│   │   ├── openai_handler.go       # Обработчики POST /v1/chat/completions, GET /v1/models
│   │   ├── openai_mapper.go        # Трансляция OpenAI <-> Gemini (SSE, tools, reasoning)
│   │   ├── anthropic_handler.go    # Обработчики POST /v1/messages, /v1/messages/count_tokens
│   │   ├── anthropic_mapper.go     # Трансляция Anthropic <-> Gemini (SSE, thinking, tools)
│   │   └── model_routing.go        # Нормализация и маршрутизация моделей
│   ├── admin/
│   │   ├── server.go               # Внутренний IPC HTTP/Socket сервер для TUI (127.0.0.1:8046)
│   │   └── client.go               # IPC клиент для взаимодействия TUI с сервером
│   └── tui/
│       ├── app.go                  # Bubbletea модель, управление клавишами и таймерами
│       ├── view.go                 # Рендеринг таблицы аккаунтов, статусов и логов активности
│       └── styles.go               # Стили Lipgloss (цвета, бейджи статусов)
├── test/
│   ├── mock_upstream.go            # Мок Google Cloud Code и OAuth серверов
│   ├── openai_test.go              # Тесты OpenAI Chat Completions (streaming, tools, reasoning)
│   ├── anthropic_test.go           # Тесты Anthropic Messages (streaming, tools, thinking)
│   ├── pool_test.go                # Тесты Round-Robin, Failover при 429, Pinning
│   └── sse_test.go                 # Тесты декодирования SSE потоков
├── go.mod
├── Makefile
├── build.sh
└── gemini-bridge.service
```

---

## 3. Детальное описание компонентов

### 3.1. Загрузчик и хранилище аккаунтов (`internal/account`)
- **Папка поиска**: `/root/gemini-bridge` (настраивается параметром `--config-dir` или переменной `CONFIG_DIR`, по умолчанию при локальном запуске проверяет текущую директорию).
- **Формат экспорта**: совместим с форматом десктопной версии AntigravityManager:
  ```json
  {
    "version": "1.0",
    "exportedAt": 1725880000,
    "accounts": [
      {
        "provider": "google",
        "email": "user@gmail.com",
        "name": "User Name",
        "token": {
          "access_token": "ya29...",
          "refresh_token": "1//...",
          "expires_in": 3599,
          "expiry_timestamp": 1725883600,
          "token_type": "Bearer",
          "project_id": "optional-project-id"
        },
        "status": "active"
      }
    ]
  }
  ```
- **OAuth Refresh**:
  - `CLIENT_ID`: Google OAuth Client ID for Antigravity (loaded from encoded constant or `ANTIGRAVITY_CLIENT_ID`)
  - `CLIENT_SECRET`: Google OAuth Client Secret for Antigravity (loaded from encoded constant or `ANTIGRAVITY_CLIENT_SECRET`)
  - Endpoint: `https://oauth2.googleapis.com/token`
  - Автоматически вызывается перед запросом, если `expiry_timestamp - now < 300` секунд, либо при получении HTTP 401.
- **Обнаружение `project_id`**:
  - Если у аккаунта отсутствует `project_id`, отправляется запрос на `https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist` с `{ "metadata": { "ideType": "ANTIGRAVITY" } }` для извлечения `cloudaicompanionProject`.
- **Сохранение состояния**:
  - Обновленные токены сохраняются атомарно (запись в `.tmp` + `rename`) в `cloud-accounts-cache.json` в папке конфига.

### 3.2. Пул и диспетчер запросов (`internal/account/pool.go`)
- Поддерживает два режима:
  1. **Auto Round-Robin (по умолчанию)**: запросы равномерно распределяются по списку активных аккаунтов.
  2. **Pinned**: все запросы направляются на выбранный пользователем аккаунт.
- **Обработка 429 (Лимиты квот)**:
  - При получении HTTP 429 от Google Cloud Code аккаунт переводится в состояние `Cooldown` на 60 секунд (или на время из заголовка `Retry-After`).
  - Пул автоматически выбирает следующий доступный аккаунт и повторяет запрос (до 3 попыток) без ошибки для клиента `hermes-agent`.

### 3.3. HTTP-прокси (`internal/proxy`)
- **Порт по умолчанию**: `:8045` (настраивается флагом `-port` или `PORT`).
- **Авторизация**: Bearer-токен проверяется по переменной `PROXY_API_KEY` (если задана; по умолчанию `sk-antigravity` или открытый режим).
- **Эндпоинты**:
  - `POST /v1/chat/completions` и `POST /chat/completions` (OpenAI совместимый).
  - `GET /v1/models` и `GET /models` (OpenAI каталог моделей).
  - `POST /v1/messages` и `POST /messages` (Anthropic Messages API).
  - `POST /v1/messages/count_tokens` (Anthropic подсчет токенов).
  - `GET /health` (проверка статуса моста).
- **Маршрутизация моделей**:
  - `claude-3-7-sonnet*`, `claude-3-5-sonnet*`, `claude-sonnet-4-6*` $\rightarrow$ `claude-sonnet-4-6-thinking`
  - `claude-3-opus*`, `claude-opus-4-6*` $\rightarrow$ `claude-opus-4-6-thinking`
  - `gpt-4o*`, `gpt-4*`, `gpt-3.5-turbo*` $\rightarrow$ `gemini-3-flash`
  - `gemini-2.5-pro*`, `gemini-3.1-pro*` $\rightarrow$ `gemini-3.1-pro-high`
  - `gemini-2.5-flash*`, `gemini-3-flash*` $\rightarrow$ `gemini-3-flash`
- **Трансляция Tool Calling**:
  - Входящие схемы инструментов OpenAI/Anthropic преобразуются в `functionDeclarations`.
  - Исходящие `functionCall` преобразуются в `tool_calls` с генерацией ID и сериализацией аргументов.
- **Трансляция Reasoning / Thinking**:
  - Части ответа с `thought: true` передаются в OpenAI как `choices[0].delta.reasoning_content`, а в Anthropic как блок `thinking_delta`.
  - Теги `<think>` очищаются, чтобы не загрязнять основной ответ модели.

### 3.4. Управляющий IPC и TUI (`internal/admin` и `internal/tui`)
- Фоновый сервер поднимает локальный HTTP-эндпоинт на `127.0.0.1:8046`.
- TUI на базе Charm Bubbletea:
  - Отображает таблицу аккаунтов со статусами (🟢 ACTIVE, 🟡 COOLDOWN, 🔴 ERROR).
  - Показывает режим работы (🔄 Round-Robin / 📌 Pinned: email).
  - Позволяет клавишами `[Enter]` или `[p]` закрепить или открепить аккаунт.
  - Позволяет клавишей `[r]` запустить обновление токена, `[R]` — перечитать папку конфигов.
  - Показывает живую панель последних 5-10 запросов.
  - Выход по `[q]` или `[Ctrl+C]` завершает только TUI-клиент, не влияя на работу сервера.

---

## 4. План тестирования и верификации

1. **Unit-тесты маппинга запросов и ответов**:
   - `openai_mapper_test.go`: проверка конвертации истории сообщений, вызовов функций, рассуждений и картинок.
   - `anthropic_mapper_test.go`: проверка корректности генерации событий Anthropic SSE.
   - `sse_test.go`: декодирование `data: {"response": ...}` и `[DONE]`.
2. **Тесты пула и отказоустойчивости**:
   - `pool_test.go`: проверка Round-Robin распределения, закрепления аккаунта, перевода в Cooldown при 429 и успешного failover на живой аккаунт.
3. **End-to-End тесты с мок-сервером Google Cloud Code**:
   - Полноценный запуск HTTP прокси в тесте.
   - Выполнение потокового и непотокового запроса от OpenAI SDK клиента.
   - Выполнение агентного цикла с вызовом инструментов (Tool Calling).
   - Выполнение запроса через Anthropic SDK клиент.
   - Проверка авторизации по API ключу.
4. **Сборка и запуск на Linux**:
   - Кросс-компиляция под `linux/amd64` и `linux/arm64`.
   - Проверка бинарника.

---

## 5. Развертывание на Linux VPS

1. Установка бинарника в `/usr/local/bin/gemini-bridge`.
2. Создание папки конфига `/root/gemini-bridge` и размещение файла `cloud-accounts-export-YYYY-MM-DD.json`.
3. Установка systemd-юнита:
   ```ini
   [Unit]
   Description=Gemini Bridge VPS Proxy for hermes-agent
   After=network.target

   [Service]
   Type=simple
   User=root
   WorkingDirectory=/root/gemini-bridge
   ExecStart=/usr/local/bin/gemini-bridge serve -port 8045 -config-dir /root/gemini-bridge
   Restart=always
   RestartSec=5
   Environment=PROXY_API_KEY=sk-antigravity

   [Install]
   WantedBy=multi-user.target
   ```
4. Запуск и управление:
   - `systemctl start gemini-bridge` — запуск сервиса.
   - `gemini-bridge` (или `gemini-bridge tui`) — запуск интерактивного TUI в консоли.
