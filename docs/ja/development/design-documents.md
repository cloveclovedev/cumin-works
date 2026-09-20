# 設計文書の書き方

設計文書は `docs/ja/designs/` に置く。featureごとに1つの、生きた文書である。ファイル名はfeatureの名前 (小文字のkebab-case) で、日付は付けない。本文には、今の意図した設計だけを書く。変更の履歴は、GitとPull Requestで追う。

## 読み手と狙い

- 読み手は、Owner、分割や実装をするAgent、このリポジトリを読む他の開発者である。背景を知らない人が読んでも、今の設計が分かるように書く。
- Agentは、実装Issueの「Related documents」のリンクから、1つの話題に直接来ることが多い。
- 要件がどう実現されるかを書く。何をするかは [要件文書](../requirements/index.md) にあり、写さない。
- 短く保つ。長い文書は読まれなくなり、直されなくなる。Agentが毎回読んでも負担にならない長さにする。

## テンプレート

```markdown
# <feature>の設計

- 状態: Draft | Approved | Superseded
- 要件: <実現する要件文書へのリンク>
- 事実の出どころ: <実測の表や、公式ドキュメントの示し方>

<この文書が何を決めるかを、1段落で書く。要件の内容は繰り返さない。>

## 範囲

扱うこと:

- ...

扱わないこと:

- ... (どこで決めるかを添える)

## 設計

### <話題。名詞句にする。番号は付けない>

- 決めたこと。理由。受け入れた不利益。
- 採らなかった案: <案>。<理由を1文で>

## まだ決めていないこと

| 決める、または確かめること | どこで |
|---|---|

## 後回しにしたこと

- <こと>。きっかけ: <いつ見直すか>
```

## 決まり

### 全体

- 1つの文書は200行までにする。超えそうなら、featureを分けるか、手順を `docs/ja/development/` に移す。
- 2番目の階層の見出しは、テンプレートの4つだけにし、名前も順序も変えない。どの設計文書でも同じ見出しで探せるようにするためである。書くことがない節は、消さずに「なし」と書く。
- 見出しに番号を付けない。節を足すと番号がずれ、リンクと「Nを参照」が壊れるためである。他の節は、名前で参照する。
- 用語は、要件文書と同じ語を使う。1つのものを、2つの名前で呼ばない。
- 太字は使わない。

### 冒頭

- 状態は3つである。Draft は、Ownerがまだ承認していない。Approved は、Ownerが承認した。Superseded は、feature全体が別の文書に置き換えられた。Superseded の文書は消さず、置き換えた文書へのリンクを書く。
- 「要件」には、この設計が実現する要件文書を書く。本文では、行の番号 (R1、I3、Q1 など) で要件と結ぶ。
- 「事実の出どころ」には、事実をどう示すかを書く。実測した事実は [調査・実測で確定した制約](../requirements/evidence/measured-constraints.md) の行の番号で、公式ドキュメントはページの名前で示す。確かめた事実と、自分の推論を分ける。確かめていないことは「未確認」と書き、「まだ決めていないこと」に載せる。
- 前置きは1段落にする。

### 範囲

- 「扱うこと」には、何をこの文書で決めるのかの基準を書く。話題の一覧にはしない。
- 「扱わないこと」には、この文書で決めてもおかしくないが、決めないことを書く。他の文書で決めるものには、その文書へのリンクを添える。「落ちない」のような、当たり前のことの否定は書かない。

### 設計

- 話題ごとに、3番目の階層の見出しを1つ立てる。見出しはリンク先になるので、なるべく変えない。
- 話題には、決めたこと、理由、受け入れた不利益、採らなかった案を、同じ場所に書く。読み手は1つの話題だけを読むことが多いためである。
- 採らなかった案は、理由を付けて1行で書く。feature全体にまたがる案は、「設計」の見出しの直後、最初の話題の前に書く。
- 決定を覆したときは、前の決定を、その話題の「採らなかった案」に理由と一緒に残す。Gitの履歴を読まないAgentが、やめた案をもう一度提案しないようにするためである。
- 流れ、状態、構成のように、ものの間の関係を表すことは、図にできるなら図にする。図は [図の描き方](diagrams.md) に従って描き、`.puml` とSVGを文書と同じディレクトリに置く。埋め込んだ図の直後に、元の `.puml` へのリンクを必ず書く。
- 図に描いたことを、文章で繰り返さない。決めたことと理由は、図ではなく文章で書く。図だけでは、理由が伝わらないためである。
- 図は、部品の間の関係の水準で描く。コードの細部の図は、コードとずれやすいので描かない。
- コードの置き場所は、パッケージの名前で書く。ファイルごとの説明は書かない。
- 次のものは書かない。
  - 要件の写し
  - スキーマやインターフェースの定義の、全文の写し
  - コード。新しいアルゴリズムを説明するときだけ、例外にする
  - 手順。`docs/ja/development/` に書く
  - 作業の進み具合。Issueに書く

### まだ決めていないこと

- 決まっていないこと、確かめていないことを、1行に1つ書く。「どこで」には、それを決める、または確かめるIssueを書く。
- 読み手は、ここにあることを推測で埋めない。
- 決まったら行を消し、結果を話題に書く。

### 後回しにしたこと

- 設計として、今はやらないと決めたことを書く。見直すきっかけを必ず添える。
- 要求の水準で後回しにしたことは、[要求のbacklog](../requirements/backlog.md) にある。写さずに、リンクする。

## 変更の履歴

設計文書の中に、変更の履歴 (Decision log) は持たない。

- 何を変えたかはGitの履歴に、なぜ変えたかはPull Requestの説明に残る。
- 履歴は増え続けるので、200行の上限を圧迫する。
- 複数のPull Requestが同じ末尾に行を足すと、mergeで衝突する。

設計を変えるときは、コードと同じPull Requestで、本文をその場で直す。新しい設計文書を作ったら、[ドキュメントの入口](../index.md) にリンクを足す。

## 出典

調査は2026-09-20に行った。「原文」は、本文の全体を取得して確かめたものである。「要約経由」は、ページを要約する取得ツールを通して確かめたものである。文言をそのまま使うときは、原文を確かめ直すこと。

| 出典 | 確かめ方 | 使った箇所 |
|---|---|---|
| Design Docs at Google https://www.industrialempathy.com/posts/design-docs-at-google/ | 原文 | 要件、定義の写し、コード、手順を書かない。扱わないことの定義。採らなかった案は短く書く。小さな設計文書は1〜3ページ |
| Kubernetes KEP https://github.com/kubernetes/enhancements/tree/master/keps/NNNN-kep-template | 原文 | 1つのfeatureを、1つの文書で、その場で直し続ける。Non-Goals。未決の箇所に目印を付ける。テンプレートだけで830行あり、長さの面では手本にしない |
| Rust RFC https://github.com/rust-lang/rfcs/blob/master/0000-template.md 、Oxide RFD 1 https://rfd.shared.oxide.computer/rfd/0001 | 原文 | 文書の中に変更の履歴を持たず、GitとPull Requestに任せる。Unresolved questions |
| Microsoft Engineering Fundamentals Playbook https://microsoft.github.io/code-with-engineering-playbook/design/design-reviews/ | 原文 | Goals、Non-Goals、Open Questions の節。decision logを勧める理由は、大きな文書から決定を探しにくいことであり、短い文書には当てはまらない |
| MADR https://adr.github.io/madr/ | 原文 | 採らなかった案を必ず書く。得失は1行で書く |
| AWS Prescriptive Guidance (ADR) https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/ 、Azure Well-Architected (ADR) https://learn.microsoft.com/en-us/azure/well-architected/architect-role/architecture-decision-record | 原文 | 書き換えない記録の型。この文書では採らなかった。採らなかった案を残すのは、同じ議論を繰り返さないため |
| Documenting Architecture Decisions https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions | 要約経由 | 1〜2ページ。大きな文書は更新されなくなる (短く保つ理由) |
| arc42 https://docs.arc42.org/home/ | 要約経由 | 概念ではなく決定を書く。採らなかった案を書く。重複を避ける。全ての節を埋めない |
| C4 model https://c4model.com/ | 要約経由 | 長く保つ図は、system context と container の2段までにする。コードの水準の図は勧めていない |
| Claude Code (memory、best practices) https://code.claude.com/docs/en/memory 、https://code.claude.com/docs/en/best-practices | 原文 | 見出しと箇条書きで構造を作る。矛盾を残さない。コードを読めば分かること、頻繁に変わること、ファイルごとの説明を書かない。範囲の外を明記する |
| Skill authoring best practices https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices | 原文 | 1つの語を使い通す。時間とともに古くなる情報を本文に置かない。参照は1段までにする |
| GitHub spec-kit https://github.com/github/spec-kit | テンプレートは原文、説明は要約経由 | 何を・なぜ (spec) と、どう作るか (plan) を分ける。未決の箇所に目印を付ける |

設計文書そのものについて、AgentのCLIの公式の手引きが述べているものは見つからなかった。上の表のClaude Codeとskillの行は、指示ファイルとskillについての手引きを、設計文書に当てはめたものである。

出典に基づかず、この文書で決めたこと:

- 200行の上限。CLAUDE.md の目安 (200行未満) からの類推である
- 2番目の階層の見出しを4つに固定し、話題を3番目の階層に置くこと
- 「影響」と「関連する文書」の節を持たず、話題の中と冒頭の行に書くこと
- 決定を覆したときに、前の決定を「採らなかった案」に残すこと
- 状態を Draft、Approved、Superseded の3つにすること
- 図にできることを、図にすること
