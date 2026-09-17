# gemini-bridge

[![GitHub Release](https://img.shields.io/github/v/release/beermix/gemini-bridge?logo=github)](https://github.com/beermix/gemini-bridge/releases)
[![CI](https://github.com/beermix/gemini-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/beermix/gemini-bridge/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/beermix/gemini-bridge)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **gemini-bridge** — высокопроизводительный сервис-прокси и мост, **разработанный специально для использования с автономным AI-агентом [hermes-agent](https://github.com/nousresearch/hermes-agent)** (а также с любыми OpenAI / Anthropic SDK). Он обеспечивает стабильный доступ к Google Antigravity Cloud Code с поддержкой пула аккаунтов, автоматической фиксации (Sticky для сохранения кэша промптов), закрепления (Pinning), отказоустойчивости (Auto-Failover / Cooldown при HTTP 429) и автоматического восстановления вызовов инструментов (Tool Call Leakage Recovery).

> [!IMPORTANT]
> **Для работы сервиса необходимо экспортировать аккаунты через [AntigravityManager](https://github.com/Draculabo/AntigravityManager)**.
> Полученный файл экспорта (`cloud-accounts-export-*.json` или переименованный в `accounts.json`) размещается в рабочей директории сервиса на VPS (по умолчанию `/root/gemini-bridge`).

---

## Оглавление

1. [Готовые сборки для Linux](#готовые-сборки-для-linux-releases)
2. [Возможности](#возможности)
3. [Архитектура](#архитектура)
4. [Быстрый старт и сборка](#быстрый-старт-и-сборка)
5. [Развертывание на Linux VPS](#развертывание-на-linux-vps)
6. [Интеграция с hermes-agent](#интеграция-с-hermes-agent)
7. [Интерактивный TUI и управление](#интерактивный-tui-и-управление)
8. [Справочник API](#справочник-api)
9. [Примеры проверки через curl](#примеры-проверки-через-curl)
10. [Переменные окружения и флаги](#переменные-окружения-и-флаги)

---

## Готовые сборки для Linux (Releases)

Готовые бинарные сборки для Linux доступны на странице [GitHub Releases](https://github.com/beermix/gemini-bridge/releases/latest).

Для быстрой установки бинарника на Linux VPS:

```bash
# Для Linux AMD64 (x86_64):
curl -sL https://github.com/beermix/gemini-bridge/releases/latest/download/gemini-bridge-linux-amd64 -o /usr/local/bin/gemini-bridge
chmod +x /usr/local/bin/gemini-bridge

# Для Linux ARM64 (aarch64 / Oracle Ampere / AWS Graviton):
curl -sL https://github.com/beermix/gemini-bridge/releases/latest/download/gemini-bridge-linux-arm64 -o /usr/local/bin/gemini-bridge
chmod +x /usr/local/bin/gemini-bridge
```

---


## Возможности

- **Полная совместимость с OpenAI API**: эндпоинты `/v1/chat/completions` и `/v1/models` с поддержкой потоковой передачи (Server-Sent Events), вызова инструментов (Tool Calling / Function Calling) и изображений (Vision).
- **Полная совместимость с Anthropic API**: эндпоинт `/v1/messages` с потоковыми событиями (`message_start`, `content_block_delta`, `message_delta`), блоками рассуждений (`thinking`) и вызовом функций.
- **Пул аккаунтов Antigravity Cloud Code**: загрузка аккаунтов, экспортированных через [AntigravityManager](https://github.com/Draculabo/AntigravityManager) (файлы `cloud-accounts-export-*.json` или `accounts.json`), автоматический кэш `cloud-accounts-cache.json`.
- **Оптимизация контекста и кэша**: режим Sticky-until-error (запросы идут на один аккаунт для максимального попадания в серверный кэш промптов Google, а при лимитах или ошибках происходит мгновенный автопереход на следующий).
- **Закрепление аккаунтов (Pinning) с персистентностью**: закрепленный аккаунт сохраняется в `config.json` и восстанавливается после перезагрузки VPS или перезапуска службы.
- **Интеллектуальный Cooldown при 429**: при превышении квоты (HTTP 429) аккаунт временно переводится в cooldown (по умолчанию 300с), а запрос автоматически перенаправляется на следующий доступный аккаунт пула.
- **Автообновление токенов**: автоматическое продление OAuth токенов до их истечения и автоматическое получение `project_id`.
- **Интерактивный терминальный интерфейс (TUI)**: мониторинг статуса аккаунтов, RPS, ошибок и переключение режима закрепления прямо в терминале VPS.
- **Минимальные требования**: скомпилированный бинарник без CGO весит менее 9 МБ и не требует внешних рантаймов (Node.js, Python и др.).

---

## Архитектура

```
[ hermes-agent / OpenAI SDK / Anthropic SDK ]
                     │
                     ▼ HTTP Bearer sk-antigravity
       ┌───────────────────────────┐
       │   gemini-bridge (:8045)   │
       │  - OpenAI / Anthropic     │
       │    Protocol Mappers       │
       │  - Account Pool & RR      │
       │  - Failover / 429 Cooldown│
       └─────────────┬─────────────┘
                     │ Google Cloud Code Internal API (v1internal)
                     ▼
       ┌───────────────────────────┐
       │ Google Antigravity        │
       │ Cloud Code Upstream       │
       └───────────────────────────┘
```

Параллельно работает встроенный внутренний IPC/Admin сервер на порту `8046`:
```
[ gemini-bridge tui / CLI status ]
              │ GET /api/status, POST /api/select, POST /api/reload
              ▼
   ┌───────────────────────┐
   │ Admin Server (:8046)  │
   └───────────────────────┘
```

---

## Быстрый старт и сборка

### Требования
- Go 1.23 или новее.

### Сборка скриптами

**На Linux / macOS / WSL / Git Bash:**
```bash
chmod +x build.sh
./build.sh
```

**На Windows:**
```cmd
build.bat
```

В результате в каталоге `bin/` будут созданы:
- `bin/gemini-bridge-linux-amd64` — для 64-битных Linux VPS (Ubuntu, Debian, CentOS и др.)
- `bin/gemini-bridge-linux-arm64` — для ARM64 Linux VPS (Oracle Ampere, Graviton и др.)
- `bin/gemini-bridge.exe` / `bin/gemini-bridge` — бинарник для локальной системы

### Ручная сборка

```bash
# Для текущей системы:
go build -ldflags="-s -w" -o bin/gemini-bridge ./cmd/gemini-bridge

# Кросс-компиляция для Linux AMD64:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/gemini-bridge-linux-amd64 ./cmd/gemini-bridge

# Кросс-компиляция для Linux ARM64:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/gemini-bridge-linux-arm64 ./cmd/gemini-bridge
```

---

## Развертывание на Linux VPS

### Шаг 1. Копирование бинарника на VPS

```bash
scp bin/gemini-bridge-linux-amd64 root@<vps-ip>:/usr/local/bin/gemini-bridge
ssh root@<vps-ip> "chmod +x /usr/local/bin/gemini-bridge"
```

### Шаг 2. Создание каталога конфигурации и загрузка аккаунтов

Создайте рабочую директорию на сервере:
```bash
ssh root@<vps-ip> "mkdir -p /root/gemini-bridge"
```

Экспортируйте файл аккаунтов через приложение [AntigravityManager](https://github.com/Draculabo/AntigravityManager) (`cloud-accounts-export-*.json` или `accounts.json`) и скопируйте его на VPS:
```bash
scp cloud-accounts-export-*.json root@<vps-ip>:/root/gemini-bridge/
```

### Шаг 3. Настройка и запуск systemd-сервиса

Скопируйте `gemini-bridge.service` в каталог служб systemd:
```bash
scp gemini-bridge.service root@<vps-ip>:/etc/systemd/system/gemini-bridge.service
```

Включите и запустите службу:
```bash
ssh root@<vps-ip>
systemctl daemon-reload
systemctl enable --now gemini-bridge
```

Проверьте статус службы:
```bash
systemctl status gemini-bridge
journalctl -u gemini-bridge -f
```

---

## Интеграция с hermes-agent

`gemini-bridge` полностью совместим с `hermes-agent`. Вы можете использовать как OpenAI-совместимый протокол, так и Anthropic-совместимый протокол.

### Вариант 1: Конфигурация OpenAI Provider

В файле конфигурации агента (например, `~/.hermes/config.yaml` или `hermes.json`):

```yaml
provider: openai
base_url: "http://<vps-ip>:8045/v1"
api_key: "sk-antigravity"
model: "claude-3-7-sonnet-20250219"
```

Или через переменные окружения:
```bash
export OPENAI_BASE_URL="http://<vps-ip>:8045/v1"
export OPENAI_API_KEY="sk-antigravity"
export DEFAULT_MODEL="claude-3-7-sonnet-20250219"
```

### Вариант 2: Конфигурация Anthropic Provider

```yaml
provider: anthropic
base_url: "http://<vps-ip>:8045"
api_key: "sk-antigravity"
model: "claude-3-7-sonnet-20250219"
```

Или через переменные окружения:
```bash
export ANTHROPIC_BASE_URL="http://<vps-ip>:8045"
export ANTHROPIC_API_KEY="sk-antigravity"
```

### Доступные псевдонимы моделей

`gemini-bridge` автоматически сопоставляет запрашиваемую клиентом модель с оптимальной моделью Google Cloud Code:

| Имя модели в запросе клиента | Целевая upstream-модель | Особенности |
|-----------------------------|-------------------------|-------------|
| `claude-3-7-sonnet-20250219` | `gemini-2.5-pro`        | Поддержка thinking blocks |
| `claude-3-5-sonnet-20241022` | `gemini-2.5-pro`        | Режим кодинга и инструментов |
| `claude-3-opus-20240229`     | `gemini-2.5-pro`        | Высокоинтеллектуальные задачи |
| `claude-3-haiku-20240307`    | `gemini-2.5-flash`      | Быстрые ответы с минимальной задержкой |
| `gpt-4o`                     | `gemini-2.5-pro`        | OpenAI совместимость |
| `gpt-4o-mini`                | `gemini-2.5-flash`      | Легковесный режим |
| `gemini-2.5-pro`             | `gemini-2.5-pro`        | Прямое обращение к Gemini 2.5 Pro |
| `gemini-2.5-flash`           | `gemini-2.5-flash`      | Прямое обращение к Gemini 2.5 Flash |

---

## Интерактивный TUI и управление

### Подключение TUI

Если сервис `gemini-bridge` запущен в фоне (например, через systemd), просто выполните на VPS:

```bash
gemini-bridge
# или явно:
gemini-bridge tui
```

`gemini-bridge` обнаружит работающий сервис и откроет интерактивную панель управления в терминале.

### Горячие клавиши TUI

| Клавиша | Действие |
|---------|----------|
| `↑` / `k` | Перемещение курсора вверх |
| `↓` / `j` | Перемещение курсора вниз |
| `Enter` или `p` | **Закрепить (Pin)**: перенаправлять все запросы только на выбранный аккаунт (сохраняется в `config.json`) |
| `u` | **Снять закрепление (Unpin)**: вернуться в автоматический режим Sticky (сохраняется в `config.json`) |
| `r` | **Обновить токен**: принудительно обновить OAuth токен выбранного аккаунта |
| `R` | **Перечитать конфигурацию**: пересканировать каталог аккаунтов на диске без рестарта |
| `q` / `Esc` / `Ctrl+C` | Выйти из TUI (фоновый прокси-сервис продолжит работу) |

> **Примечание о персистентности:** при закреплении аккаунта (`Enter`/`p`) или снятии закрепления (`u`) выбор сразу сохраняется в `config.json` в каталоге `-config-dir`. При перезагрузке VPS или рестарте службы `gemini-bridge` автоматически восстановит выбранный аккаунт.

### Неинтерактивный статус (для SSH и скриптов)

Для получения мгновенного статуса без интерактивного TUI:

```bash
gemini-bridge status
```

Вывод:
```
================================================================
 Gemini-Bridge Service Status (http://127.0.0.1:8046)
================================================================
Mode     : STICKY
Uptime   : 2h 45m
Requests : Total: 1420 | Success: 1412 | 429 Cooldown: 6 | Errors: 2
----------------------------------------------------------------
Accounts (3):
  #   EMAIL / ID                       STATUS     COOLDOWN     PROJECT ID
  1   dev1@gmail.com                   active     none         gen-lang-client-01
  2   dev2@gmail.com                   cooldown   2m40s        gen-lang-client-02
  3   dev3@gmail.com                   active     none         gen-lang-client-03
================================================================
```

Для скриптов автоматизации доступен JSON-формат:
```bash
gemini-bridge status -json
```

---

## Справочник API

### Основной порт прокси (по умолчанию `:8045`)

Требуется заголовок `Authorization: Bearer <api-key>`.

- `POST /v1/chat/completions` — создание чат-дополнения (OpenAI Chat API). Поддерживает `stream: true/false`, `tools`, `tool_choice`, `messages` (включая `image_url`).
- `GET /v1/models` — список доступных моделей OpenAI.
- `POST /v1/messages` — генерация сообщений (Anthropic Messages API). Поддерживает `stream: true/false`, `system`, `tools`, `thinking`.
- `POST /v1/messages/count_tokens` — подсчет токенов для сообщений Anthropic.
- `GET /health` — проверка состояния сервиса (не требует авторизации).

### Порт IPC/Администрирования (по умолчанию `127.0.0.1:8046`)

Локальный внутренний API для TUI и внешнего мониторинга:

- `GET /api/status` — полный статус сервиса, режим работы, список аккаунтов и статистика.
- `POST /api/select` — закрепление аккаунта или возврат в Sticky-режим (`{"account_id":"...", "unpin":false}`).
- `POST /api/reload` — горячая перезагрузка файлов аккаунтов с диска.
- `POST /api/refresh` — обновление OAuth токена конкретного аккаунта (`{"account_id":"..."}`).

---

## Примеры проверки через curl

### 1. Проверка состояния сервиса (/health)

```bash
curl -s http://127.0.0.1:8045/health | jq .
```

### 2. Запрос в формате OpenAI API

```bash
curl -X POST http://127.0.0.1:8045/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-antigravity" \
  -d '{
    "model": "claude-3-7-sonnet-20250219",
    "messages": [
      {"role": "user", "content": "Привет! Напиши одно предложение о космосе."}
    ],
    "temperature": 0.7
  }'
```

### 3. Потоковый запрос в формате OpenAI (Streaming)

```bash
curl -N -X POST http://127.0.0.1:8045/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-antigravity" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      {"role": "user", "content": "Расскажи короткую историю."}
    ],
    "stream": true
  }'
```

### 4. Запрос в формате Anthropic API

```bash
curl -X POST http://127.0.0.1:8045/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-antigravity" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-3-7-sonnet-20250219",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Explain quantum computing in one paragraph."}
    ]
  }'
```

---

## Переменные окружения и флаги

| Флаг | Переменная окружения | По умолчанию | Описание |
|------|----------------------|--------------|----------|
| `-port` | `PROXY_PORT` | `8045` | Порт входящих запросов прокси |
| `-admin-port` | `ADMIN_PORT` | `8046` | Порт локального админ-сервера IPC |
| `-config-dir` | `CONFIG_DIR` | `/root/gemini-bridge` | Каталог с файлами аккаунтов (fallback на текущую директорию) |
| `-pinned-account` | `PINNED_ACCOUNT` | `""` | Начальный pinned аккаунт (ID или Email, переопределяет конфиг) |
| `-api-key` | `PROXY_API_KEY` | `sk-antigravity` | Секретный ключ авторизации для клиентов |
| `-proxy-url` | `HTTPS_PROXY` / `HTTP_PROXY` | `""` | Исходящий прокси для запросов к Google API |
| `-admin-url` | `ADMIN_URL` | `http://127.0.0.1:8046` | URL админ-сервера для `tui` и `status` |

---

## Лицензия

MIT License
