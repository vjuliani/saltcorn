# ADR-0006 — Critérios de retirada do backend legado Node e do host de plugins

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §3 (fim) e §5 "Conclusão da migração"](../README.md), [ADR-0001](0001-backend-go-cqrs.md), [ADR-0003 (BFF, permanente — não coberto por este ADR)](0003-bff-nodejs-permanente.md), [ADR-0005 (extensões)](0005-politica-de-extensoes.md)

## Contexto

O README trata explicitamente **dois componentes temporários distintos**, cada um com seu próprio plano de retirada, e um terceiro componente que **não é temporário**:

- **Backend legado Node** (o domínio atual em `packages/saltcorn-data`/`packages/server`): temporário, será substituído por capacidade/tenant.
- **Host temporário de plugins JS** ([ADR-0005](0005-politica-de-extensoes.md)): "componente separado do BFF e tem seu próprio plano de retirada" (README, citação direta).
- **BFF Node.js**: **não é temporário** — "faz parte da solução final" (README §5). Não confundir "retirar o legado" com "eliminar Node.js do sistema".

Sem critérios explícitos e verificáveis, "a migração está terminando" vira uma afirmação subjetiva. Este ADR converte as condições já espalhadas pelo README (§3, §5) em critérios de saída específicos por componente.

## Decisão

### Critérios de retirada do backend legado Node (domínio)

O backend legado só é retirado, por aplicação/tenant/capacidade, quando **todas** as condições abaixo são verdadeiras:

1. Toda capacidade usada pelo escopo contratado dessa aplicação está classificada como nativa em Go **ou** explicitamente aceita como bloqueador de escopo (não migrada por decisão, não por omissão) — README §3 item 1.
2. O canário por tenant/capacidade está operando de forma estável no roteamento para Go (README §3 item 6).
3. Dados e metadados escritos por Go e por Node (durante a transição) estão reconciliados — sem duas fontes de verdade divergentes.
4. Recuperação foi testada dentro do RPO/RTO definidos para a operação (README §5).
5. Nenhuma dependência restante do backend legado **ou** do host temporário de plugins de domínio para essa aplicação/tenant (README §5, condição explícita e conjunta — depender de qualquer um dos dois significa migração ainda em andamento).

**Condição de bloqueio do rollback:** rollback de roteamento (voltar tráfego para o Node legado) só é permitido se o Node compreender os dados e metadados já escritos por Go. Caso contrário, a ação correta é pausar escritas e executar a reconciliação/restauração ensaiada — **retornar o tráfego sozinho não reverte dados** (README §3 item 6, citação direta). Isso significa que a retirada do legado, uma vez além de certo ponto por capacidade, deixa de ter um "botão de desfazer" barato.

### Critérios de retirada do host temporário de plugins JS

O host de plugins é retirado, para o escopo contratado, quando:

1. Todos os plugins usados por esse escopo foram portados nativamente para Go, adaptados via adapter compatível, ou explicitamente bloqueados/aceitos para o corte (não seguem em uso sem decisão) — matriz de plugins por versão atualizada (GO-022/GO-023 dependem desta matriz estar corrente).
2. O domínio é considerado **integralmente migrado para Go** apenas depois de retirar esse host para o escopo contratado (README §3, citação direta) — isto é, a retirada do host de plugins é uma pré-condição para declarar o domínio "migrado", não uma consequência automática dela.

### O que este ADR **não** cobre

- O BFF Node.js. Ele não tem critério de retirada porque não é temporário ([ADR-0003](0003-bff-nodejs-permanente.md)) — este ADR existe justamente para que ninguém tente aplicar os critérios acima ao BFF por engano.
- SQLite/mobile: sua conclusão de escopo é tratada em [ADR-0004](0004-estrategia-de-bancos.md) (etapas F5), não é uma "retirada" no sentido deste documento.

## Custos operacionais

- Manter a matriz de plugins por versão viva (não um levantamento único) é custo operacional contínuo enquanto o host de plugins existir — sem ela, o critério 1 de retirada do host nunca pode ser verificado com confiança.
- A "condição de bloqueio do rollback" acima implica que a operação precisa ter um procedimento de reconciliação/restauração **ensaiado antes** de avançar o canário além de um certo ponto — ensaiar depois de precisar é tarde demais.
- Declarar uma aplicação/tenant "migrada" exige checar 5 condições simultaneamente (backend) mais 2 (plugins) — isso é trabalho de auditoria recorrente, não uma checklist de uma vez.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| Declarar a migração concluída quando o corpus de testes críticos passar em Go | Insuficiente: não cobre reconciliação de dados já escritos, RPO/RTO ensaiado, nem a condição conjunta de nenhuma dependência do host de plugins |
| Tratar BFF Node.js como "a próxima coisa a remover" após o domínio migrar | Rejeitado explicitamente: o BFF é parte da arquitetura final (README §5); confundir isso levaria a planejar um trabalho que não deve acontecer |
| Permitir rollback de roteamento a qualquer momento como rede de segurança | Rejeitado: rollback sem garantia de que o Node compreende os dados escritos por Go pode mascarar perda/corrupção de dados; a política exige reconciliação ensaiada, não apenas reverter tráfego |

## Consequências e riscos

- Enquanto qualquer capacidade de uma aplicação depender do backend legado ou do host de plugins, essa aplicação é, por definição, "migração ainda em andamento" — não existe um estado intermediário de "quase migrado" reconhecido por este ADR.
- Um canário revertido sem reconciliação, feito sob pressão operacional, viola a condição de bloqueio do rollback deste ADR e deve ser tratado como incidente, não como operação de rotina.
