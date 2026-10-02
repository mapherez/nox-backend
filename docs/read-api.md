# API de leitura — integração com o Codex

Este contrato pertence ao NoX Backend (`mapherez/nox-backend`). A separação do repositório preserva as rotas, formatos, autenticação e comportamento existentes. O plugin Obsidian continua em [mapherez/nox-sync](https://github.com/mapherez/nox-sync).

Este contrato permite ao servidor Admin do Codex usar o **Server URL** e a
**API key** já existentes no NoX Sync:

**ligar → escolher vault → listar ficheiros → descarregar versões selecionadas → importar como drafts no Codex**.

A atualização é apenas do backend NoX Sync. O importador, a seleção e a
interpretação dos ficheiros, a criação de drafts e o Publish pertencem ao Codex
e serão implementados no outro repositório. O NoX Sync não gere publicação.
Não são necessários novos controlos no dashboard nem alterações no plugin Obsidian.

## Autenticação e escolha do vault

Todos os pedidos usam a autenticação existente:

```http
Authorization: Bearer <API_KEY>
```

O Server URL é a URL base do backend, sem acrescentar `/v1` na configuração.
Os exemplos abaixo usam apenas um domínio, identificadores, hashes e credenciais
fictícios. Substitua-os pelos valores da instalação.

```bash
SERVER_URL='https://nox-sync.example.invalid'
API_KEY='nox_fictitious_example_key'

curl --fail --show-error \
  -H "Authorization: Bearer $API_KEY" \
  "$SERVER_URL/v1/auth/check"

curl --fail --show-error \
  -H "Authorization: Bearer $API_KEY" \
  "$SERVER_URL/v1/vaults"
```

`GET /v1/auth/check` valida a chave e devolve o formato existente, incluindo
`ok`, `user` e `role`. `GET /v1/vaults` mantém o contrato existente: a propriedade
`vaults` contém os vaults ativos do utilizador, com `vaultId`, `name`, `revision`,
`status`, `updatedAt` e `sizeBytes`. A resposta pode também conter
`deletedVaults`; o consumidor deve escolher apenas entre os vaults ativos.

O acesso fica limitado aos vaults do utilizador associado à chave, mesmo que
tenha papel `ADMIN`. O papel Admin no Codex também não concede acesso adicional
no NoX Sync. Não há novas credenciais nem permissões globais de leitura.
Uma chave inválida ou antiga após rotação, ou uma chave de um utilizador
desativado, recebe `401`.

## Listar os ficheiros atuais

```http
GET /v1/files?vaultId=<ID>
```

```bash
curl --fail --show-error --get \
  -H "Authorization: Bearer $API_KEY" \
  --data-urlencode 'vaultId=vault_example' \
  "$SERVER_URL/v1/files"
```

Resposta `200`, com `Content-Type: application/json` e
`Cache-Control: no-store`:

```json
{
  "vaultId": "vault_example",
  "serverRevision": 12,
  "files": [
    {
      "path": "World/Humans.md",
      "hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "size": 2048,
      "revision": 10
    }
  ]
}
```

- `files` contém todos os ficheiros atuais não eliminados, de qualquer tipo,
  incluindo Markdown, imagens, outros binários e ficheiros de tamanho zero.
- Os caminhos são relativos ao vault e usam `/`, preservando os nomes e a
  capitalização armazenados. Espaços, Unicode e nomes iguais em pastas diferentes
  são permitidos. Codifique os parâmetros da URL, como nos exemplos.
- A lista é plana, ordenada por caminho e sem paginação. O consumidor reconstrói
  a árvore de pastas; pastas vazias não são representadas.
- `hash` é o SHA-256 dos bytes originais, em 64 caracteres hexadecimais minúsculos.
  `size` é o tamanho em bytes e `revision` identifica a revisão do ficheiro.
- Não há conteúdo, histórico, tombstones, uploads em staging ou caminhos
  internos do host na resposta.

Um vault vazio devolve `files: []`, incluindo quando todos os seus ficheiros
foram eliminados. Um vault novo tem `serverRevision: 0`.

### Revisão do vault e revisão do ficheiro

`serverRevision` é a revisão global **desse vault**, avançada pelos commits que
alteram o estado remoto. A `revision` de um ficheiro é a revisão do commit que
criou ou alterou esse ficheiro. Não é um contador independente por ficheiro.

No exemplo, o vault está na revisão 12 e `World/Humans.md` permanece na revisão
10. Alterações noutros ficheiros podem avançar `serverRevision` sem alterar a
revisão ou o hash de `World/Humans.md`. O download condicionado compara a revisão
individual do ficheiro, nunca a revisão global do vault.

Autorização do vault, revisão global e metadados da lista são lidos na mesma
transação SQLite. A resposta representa um estado confirmado coerente, mesmo
quando ocorre um commit concorrente.

## Descarregar a versão selecionada

```http
GET /v1/files/download?vaultId=<ID>&path=<PATH>&expectedHash=<HASH>&expectedRevision=<REVISION>
```

Os parâmetros existentes `vaultId` e `path` continuam obrigatórios. Os novos
parâmetros são opcionais e independentes:

| Parâmetro | Validação | Condição |
| --- | --- | --- |
| `expectedHash` | SHA-256 em 64 caracteres hexadecimais minúsculos | Tem de corresponder ao hash atual do ficheiro. |
| `expectedRevision` | Inteiro decimal não negativo, até `9223372036854775807` | Tem de corresponder à revisão atual do ficheiro. |

Se presentes, os parâmetros não podem ser vazios ou repetidos. Revisões com
sinal, espaços, frações ou valores fora do intervalo recebem `400`. Recomenda-se
que o Codex envie **ambas** as condições copiadas da listagem, para identificar
também alterações que tenham voltado aos mesmos bytes numa revisão posterior.

```bash
curl --fail --show-error --get \
  -H "Authorization: Bearer $API_KEY" \
  --data-urlencode 'vaultId=vault_example' \
  --data-urlencode 'path=World/Humans.md' \
  --data-urlencode 'expectedHash=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  --data-urlencode 'expectedRevision=10' \
  --dump-header download-headers.txt \
  --output Humans.md \
  "$SERVER_URL/v1/files/download"
```

Em caso de sucesso, a resposta `200` contém os bytes originais, sem transformação,
com os headers existentes:

```http
Content-Type: application/octet-stream
Content-Length: 2048
X-NoX-Sync-Path: World/Humans.md
X-NoX-Sync-Hash: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
X-NoX-Sync-Revision: 10
Cache-Control: no-store
```

O backend compara as condições com os mesmos metadados que selecionam o blob
e serve esse blob pelo hash validado. Não volta a consultar a versão mais recente
para substituir o conteúdo selecionado. O consumidor pode conferir os bytes
recebidos com o hash, tamanho e headers da seleção.

Sem `expectedHash` e `expectedRevision`, mantém-se o download atual usado pelo
plugin: devolve a versão atual encontrada durante o pedido. A exportação ZIP
existente também permanece compatível.

### Alterações entre listagem e download

Se uma condição válida não corresponder à versão atual, a resposta é `409`,
com `Content-Type: application/json`, `Cache-Control: no-store` e o formato de
erro existente:

```json
{
  "code": "FILE_CHANGED",
  "message": "File changed since selection. Refresh the file list and select again."
}
```

Não são enviados bytes do ficheiro nesta resposta. O consumidor deve tratar
o código `FILE_CHANGED`, voltar a listar o vault e atualizar a seleção antes de
tentar novamente. Não deve retirar as condições para aceitar silenciosamente
uma versão diferente. A mensagem é informativa; o código é o identificador do erro.

Se o ficheiro foi eliminado ou renomeado para outro caminho, o caminho antigo
recebe `404` (`NOT_FOUND`), mesmo com condições. Atualize a listagem para localizar
o novo caminho. Alterações noutros ficheiros não impedem descarregar a seleção
quando o seu hash e a sua revisão permanecem iguais.

Não existem snapshots persistentes nem reserva de versões históricas. A lista
é consistente por pedido; vários downloads não formam um snapshot atómico de
todo o vault. Um commit posterior à validação do download não faz o backend
trocar o blob que já selecionou para esse pedido.

## Erros e efeitos sobre o sync

| HTTP | Código | Situação |
| --- | --- | --- |
| `400` | `BAD_REQUEST` | Parâmetros obrigatórios em falta, caminho inválido ou condições malformadas. |
| `401` | `AUTH_REQUIRED` / `AUTH_FAILED` | Autenticação em falta ou inválida, chave rodada ou utilizador desativado. |
| `404` | `NOT_FOUND` | Vault inexistente, eliminado ou de outro utilizador; ficheiro inexistente, eliminado ou renomeado. |
| `409` | `FILE_CHANGED` | Hash ou revisão fornecidos não correspondem à versão atual. |
| `405` | `BAD_REQUEST` | Método diferente de `GET` nas rotas de ficheiros. |

As respostas de listagem e download, incluindo erros, usam
`Cache-Control: no-store`. A autenticação e a validação de caminhos reutilizam
o comportamento existente.

A leitura é permitida durante uma sessão de sync e mostra apenas o último
estado confirmado por commit. Não adquire locks de sync, não inicia sessões,
não altera revisões, não limpa locks expirados e não emite eventos de sync.
Este fluxo não exporta histórico, não inicia sincronização automática nem
efetua escrita no NoX Sync.
