# Instalação do agent.exe no Windows (produção)

Procedimento pra colocar o `agent.exe` rodando no servidor Windows de um cliente — conectando ao SaaS e gravando arquivos em `C:\rede\clientes\...`.

Esse doc cobre o caminho completo: compilar o binário no Mac, copiar pra VM/servidor Windows, configurar, instalar como Windows Service.

## Pré-requisitos

- **No Mac/Linux** (máquina de quem está fazendo o deploy):
  - Go 1.26+ instalado
  - Acesso ao repo `pier`

- **No servidor Windows do cliente**:
  - Windows 10/11 ou Windows Server
  - Conta com privilégio de administrador (pra instalar serviço)
  - PowerShell 5+
  - Pasta `C:\rede\clientes` criada (ou outra que vai ser o `base_path`)
  - Acesso à internet outbound HTTPS+WSS (porta 443)

- **Lado da nuvem** já configurado:
  - SaaS endpoint reachable from the customer network
  - SaaS-side configured with the auth token the agent will use
  - SaaS DNS resolvable from the agent host (cert wildcard já cobre)

## 1. Compilar o binário no Mac

```bash
cd ~/Projects/gustavoguarda/pier

# Confere que está na branch principal e com último commit
git pull

# Build pra ARM64 (Windows ARM, ex.: VM no Apple Silicon)
make build-windows-arm64

# OU pra AMD64 (servidor Windows tradicional Intel/AMD)
make build-windows
```

Resultado:
- ARM64: `dist/agent.exe` (~6.5MB)
- AMD64: `dist/agent.exe`

> ⚠️ Cada vez que houver mudança em `cmd/agent/` ou `internal/`, refaz o build e copia o novo binário pra VM. Conferir data:
> ```bash
> ls -la dist/agent.exe
> ```
> Se a data for anterior ao último commit que tocou no agent, está desatualizado.

## 2. Copiar pra VM Windows

Métodos comuns:

- **VM compartilha pasta com host** (UTM/VMware): copia direto pra pasta compartilhada
- **scp** (se VM tem SSH habilitado)
- **Pen drive/network share**
- **GitHub Releases** (futuro: subir binários assinados como release)

Destino sugerido na VM: `C:\pier\agent.exe` (ou `agent.exe` pra AMD64).

> ⚠️ Confere data do exe na VM depois de copiar — `dir` no PowerShell deve mostrar data recente. **Se mostrar data antiga, você copiou no lugar errado ou faltou substituir.**

## 3. Criar `config.yaml` na VM Windows

Caminho recomendado: `C:\pier\config.yaml`

Conteúdo (substituir `<slug>` e `<TOKEN>` pelos valores reais):

```yaml
# WebSocket do storage SaaS do tenant.
saas_url: wss://storage-<slug>.example.com/agent/ws

# HTTP base do mesmo storage.
http_base_url: https://storage-<slug>.example.com

# Token compartilhado com o storage (mesmo valor de STORAGE_SAAS_TOKEN
# nas env vars e de storage_api_token na tenant config).
token: <TOKEN>

# Pasta-raiz onde os arquivos vão ser gravados.
base_path: C:\rede\clientes
```

Concrete example:
```yaml
saas_url: wss://saas.example.com/agent/ws
http_base_url: https://saas.example.com
token: 4f8a3b9c2d1e7f0a5b6c8d9e2f3a1b4c
base_path: C:\rede\clientes
```

## 4. Testar interativamente (recomendado antes de virar serviço)

**Sempre teste interativamente primeiro.** Se falhar, os erros aparecem na tela. Se você pular direto pro modo serviço, Windows Service esconde os erros num log obscuro.

PowerShell como **Administrador** (clique-direito no PowerShell → "Run as Administrator"):

```powershell
cd C:\pier
.\agent.exe -config config.yaml
```

Saída esperada (em ~2-3 segundos):
```
INFO connecting to SaaS  url=wss://saas.example.com/agent/ws
INFO connected to SaaS
```

Em outra aba/máquina, **confirma o lado da nuvem**:
```bash
curl https://saas.example.com/healthz
# Esperado: {"status":"ok","agent_connected":true}
#                                         ^^^^
```

Pra parar o teste: **Ctrl+C** na janela do PowerShell.

### Erros comuns no teste interativo

| Mensagem | Causa | Fix |
|---|---|---|
| `connectex: No connection could be made` | URL errada ou firewall bloqueando | Confere `saas_url` e teste DNS: `nslookup saas.example.com` |
| `unauthorized` ou `401` | Token errado | Confere `token` no yaml vs `STORAGE_SAAS_TOKEN` |
| `tls: failed to verify certificate` | Cert TLS quebrado/expirado | Confere se cert wildcard ainda válido (acessa `/healthz` no browser) |
| `bad handshake` | URL com path errado | Deve ser `/agent/ws` no final |
| Trava sem mostrar nada | DNS lento ou bloqueado | Espera ~30s; se nada, problema de rede |

## 5. Instalar como Windows Service

Depois de funcionar no teste interativo, instalar como serviço pra rodar em background, auto-iniciar com Windows e sobreviver a logout.

**PowerShell como Administrador**:

```powershell
cd C:\pier

# Se já tem instalado, desinstala antes
.\agent.exe -service stop
.\agent.exe -service uninstall

# Instala — use path ABSOLUTO no -config (ver nota abaixo)
.\agent.exe -config C:\pier\config.yaml -service install

# Inicia
.\agent.exe -service start
```

> ⚠️ **Por que path absoluto?** O Windows Service inicia o exe com working directory em `C:\Windows\System32`, não na pasta do exe. Se você usar `-config config.yaml` (relativo), o agent vai procurar em `System32\config.yaml` e falhar. Builds a partir do commit que fixou isso (`fix(agent): resolve config path to absolute`) resolvem automaticamente, mas usar path absoluto sempre funciona.

Ações suportadas pelo `-service`:

| Ação | Efeito |
|---|---|
| `install` | Cria serviço no Windows (`sc create`). Não inicia ainda. |
| `start` | Inicia o serviço já instalado. |
| `stop` | Para o serviço. |
| `restart` | Stop + start. |
| `uninstall` | Remove o serviço. |

> ⚠️ **Não existe** `-service status`. Pra ver status, usa PowerShell ou Services.msc (próxima seção).

## 6. Verificar status do serviço

Várias formas, escolhe a que preferir:

### PowerShell
```powershell
Get-Service pier-agent
# Esperado:
# Status   Name                DisplayName
# ------   ----                -----------
# Running  Pier Agent
```

### sc query
```powershell
sc query pier-agent
```

### Services.msc (interface gráfica)
- `Win+R` → `services.msc` → procura "Pier Agent" na lista
- Status: **Running** (em execução)

### Confirma do lado da nuvem
```bash
curl https://saas.example.com/healthz
# Quando agent estiver conectado:
# {"status":"ok","agent_connected":true}
```

`agent_connected:true` confirma que a conexão WebSocket está ativa.

## 7. Ver logs do serviço

Como o serviço roda em background, os logs vão pro **Event Viewer** do Windows.

### Event Viewer (GUI)
- `Win+R` → `eventvwr.msc`
- Vai em **Windows Logs → Application**
- Filtra por **Source = pier** (ou pela coluna Source, ordena alfabético)

### PowerShell
```powershell
Get-EventLog -LogName Application -Source pier -Newest 20 |
  Format-Table TimeGenerated, EntryType, Message -AutoSize -Wrap
```

### Filtros úteis
```powershell
# Só erros do agent
Get-EventLog -LogName Application -Source pier -EntryType Error -Newest 20

# Últimos 30 minutos
Get-EventLog -LogName Application -Source pier -After (Get-Date).AddMinutes(-30)
```

## 8. Troubleshooting

### Serviço install OK mas start falha com timeout
> `Failed to start... The service did not respond to the start or control request in a timely fashion`

Causas comuns:
1. **Binário antigo na VM** — você copiou um exe desatualizado. Confere `dir` e compara com data do `make build-windows-arm64` no Mac.
2. **`config.yaml` faltando ou inválido** — agent crasha no startup. Roda interativamente (passo 4) pra ver o erro real.
3. **Sem rede ao DNS do storage** — agent trava no `dial`. Testa com `Test-NetConnection saas.example.com -Port 443`.

### `Failed to status` com `Unknown action status`
A ação `status` não existe no binário. Use `Get-Service` (passo 6) em vez disso.

### `Access is denied` no install
PowerShell precisa estar como **Administrador**. Clique-direito → Run as Administrator.

### Agent parece conectado mas upload falha com 404
- Confere a tabela `tenants` no banco do Laravel: `storage_api_url` deve bater com a URL pública do storage.
- Confere que `token` no yaml = `STORAGE_SAAS_TOKEN` = `storage_api_token` no DB.

### Forçar reconectar sem stop/start
Mata o processo do Windows Service e Service Manager religa em 1s:
```powershell
Get-Process | Where-Object { $_.ProcessName -like "agent*" } | Stop-Process -Force
```

## 9. Atualizar o binário no futuro

Quando precisar atualizar (novo build, fix de bug, etc):

```powershell
cd C:\pier

# Para o serviço
.\agent.exe -service stop

# (Substitui agent.exe com o novo binário copiado do Mac)

# Reinicia
.\agent.exe -service start
```

Não precisa `uninstall + install` — só substituir o binário e dar `start` de novo.

## 10. Desinstalação completa

```powershell
cd C:\pier
.\agent.exe -service stop
.\agent.exe -service uninstall

# Opcional: apaga os arquivos
Remove-Item C:\pier -Recurse -Force
```

> ⚠️ **`C:\rede\clientes` não é apagado** pelo uninstall — são os arquivos dos clientes, ficam intactos.

---

## Checklist resumido

- [ ] No Mac: `make build-windows-arm64` (ou `make build-windows`)
- [ ] Copiou `dist/agent.exe` pra `C:\pier\` da VM (data recente!)
- [ ] Criou `C:\pier\config.yaml` com `saas_url`, `http_base_url`, `token`, `base_path`
- [ ] Testou interativamente: `agent.exe -config config.yaml` → mostrou `connected to SaaS`
- [ ] `/healthz` retornou `agent_connected:true` durante o teste
- [ ] Ctrl+C pra parar o teste
- [ ] Instalou como serviço: `-service install` → `-service start`
- [ ] `Get-Service pier` mostra `Running`
- [ ] `/healthz` mostra `agent_connected:true` depois do serviço iniciar
- [ ] Fez um upload pela SaaS UI → arquivo apareceu em `<base_path>\...`
