# Testando o agente em Windows via UTM

Como o agente roda em produção como **Windows Service** on a customer on-prem server, o teste mais fiel é numa VM Windows real. Em Mac com Apple Silicon, o UTM ([mac.getutm.app](https://mac.getutm.app)) é a forma mais leve de subir essa VM.

Este guia assume Apple Silicon (M1/M2/M3/M4) — para Intel troque "ARM64" por "x64" onde indicado.

## 1. Pré-requisitos no host (Mac)

- [UTM](https://mac.getutm.app) instalado
- Imagem do **Windows 11 ARM64** baixada da galeria (`mac.getutm.app/gallery/windows-11`) — Apple Silicon roda nativamente
- Repositório clonado e `make tidy` rodado

## 2. Subir a VM no UTM

1. Importe o `.utm` da galeria (duplo clique abre direto no UTM)
2. Antes do primeiro boot, em **Edit → Network**:
   - Modo: **Shared Network** (NAT, padrão — basta para este teste)
   - Anote o gateway que o UTM vai usar (geralmente `192.168.64.1` em macOS Apple Virtualization, ou `10.0.2.2` em modo QEMU). Esse é o **IP do host visto de dentro da VM**.
3. Boot e finalize a configuração inicial do Windows (conta local serve)
4. Dentro do Windows, abra o **PowerShell como Administrador** — todos os comandos abaixo rodam nele.

## 3. Build do agente para a VM

No host (Mac), na raiz do repositório:

```bash
# Windows 11 ARM64 (recomendado em Apple Silicon — nativo, mais rápido)
make build-windows-arm64
# → dist/agent.exe

# Alternativa: Windows x64 (roda via emulação no Windows 11 ARM64)
make build-windows
# → dist/agent.exe
```

## 4. Levar o build pra dentro da VM

Mais simples — **drag-and-drop** funciona nativamente no UTM com SPICE Tools instalado (Windows 11 ARM64 da galeria já vem pronto). Arraste para dentro da VM:

- `dist/agent.exe` (ou `dist/agent.exe`)
- `config.example.yaml`

Sugestão de destino dentro do Windows: `C:\pier\`.

```powershell
mkdir C:\pier
# arraste os arquivos para C:\pier
cd C:\pier
copy config.example.yaml config.yaml
```

## 5. Configurar o `config.yaml` da VM

Edite `C:\pier\config.yaml` apontando para o `saas-stub` rodando no host:

```yaml
saas_url: ws://192.168.64.1:9000/agent/ws    # IP do gateway anotado no passo 2
http_base_url: http://192.168.64.1:9000
token: dev-token
base_path: C:\rede\clientes
```

Crie o diretório de destino:

```powershell
mkdir C:\rede\clientes
```

## 6. Subir o `saas-stub` no host (Mac)

Em outro terminal no Mac:

```bash
go run ./cmd/mocksaas
# escuta em :9000 em todas as interfaces
```

> Se o macOS pedir permissão de firewall, autorize. O `saas-stub` faz `ListenAndServe(":9000", ...)`, então já escuta em `0.0.0.0:9000`.

## 7. Rodar o agente — modo interativo primeiro

Antes de instalar como serviço, rode em foreground para ver os logs e confirmar a conexão:

```powershell
cd C:\pier
.\agent.exe -config config.yaml
```

Esperado nos logs do agente:

```
level=INFO msg="connected to saas" url=ws://192.168.64.1:9000/agent/ws
```

Esperado nos logs do `saas-stub`:

```
level=INFO msg="agent connected" remote=192.168.64.x:....
```

Se aparecer `connection refused` ou `i/o timeout`, é rede — confirme:
- Mac firewall liberou o `saas-stub`
- O IP do gateway está certo (`ipconfig` no PowerShell mostra "Default Gateway")

## 8. Testar end-to-end

Com agente conectado, no Mac (terceiro terminal):

```bash
# upload — SaaS → agente escreve em C:\rede\clientes\Cliente A\teste.txt
echo "do mac para a VM" > /tmp/teste.txt
curl -X POST http://localhost:9000/upload \
  -F "client_name=Cliente A" \
  -F "file=@/tmp/teste.txt"
```

Na VM (PowerShell):

```powershell
type "C:\rede\clientes\Cliente A\teste.txt"
# do mac para a VM
```

Download (caminho inverso — agente lê arquivo da VM e o `saas-stub` devolve pro `curl`):

```bash
curl "http://localhost:9000/download?client_name=Cliente%20A&filename=teste.txt"
# do mac para a VM
```

## 9. Instalar como Windows Service

Quando o teste interativo passar, derrube o agente (Ctrl+C) e use o script `scripts/install.ps1` (copie do repo junto com o `.exe` e `config.yaml`):

```powershell
# PowerShell como Administrador, em C:\pier
.\install.ps1                              # usa agent.exe (amd64) por padrão
.\install.ps1 -AgentExe .\agent.exe  # build ARM64 nativo
```

O script é idempotente — pode rodar de novo após recompilar; ele para o service, desinstala, e reinstala. Faz também:
- `Unblock-File` no `.exe` (limpa flag de "veio da internet" que escala SmartScreen)
- Exclusão do Defender no diretório do agente
- Cria `C:\rede\clientes\` se não existir

Verifique:

```powershell
Get-Service pier
```

Logs do serviço aparecem no **Event Viewer → Windows Logs → Application** (filtrar pela source `pier`).

Para limpar manualmente:

```powershell
.\agent.exe -service stop
.\agent.exe -service uninstall
```

## Troubleshooting

| Sintoma | Causa provável |
|---|---|
| `dial tcp 192.168.64.1:9000: connectex: ...` | saas-stub não rodando, ou firewall do macOS bloqueando |
| Agente conecta e desconecta em loop | `token` divergente entre `config.yaml` da VM e `-token` do `saas-stub` |
| `path traversal` na escrita | `base_path` deve existir e o `client` não deve conter `..` |
| Service start falha sem log no console | Os erros vão para o Event Viewer, não para stdout — confira lá |
