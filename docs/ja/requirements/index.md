# 要求と要件

cumin works v2 の要求と要件の文書の入口。

## 全体

- [要求仕様書](overview.md): 解決したい課題、目的、指標、登場人物、フェーズ
- [cumin本体の要件](cumin-core.md): ルールだけで動くワークフローの基盤。受け持つこと、動かし方、設定、通知
- [要求のbacklog](backlog.md): v0.1には入れないと決めたが、あとで反映したいこと

## ワークフロー

- [メインワークフローの図](workflow/main-workflow.svg) (元ファイル: [main-workflow.puml](workflow/main-workflow.puml)): 要求の受付から受け入れまでの流れ
- [Issueのラベルと状態遷移](workflow/issue-states.md): 要求Issueと実装Issueの状態を表すラベル、状態が移る条件、cuminの動作のきっかけ

## Agentの要件

- [Agentに共通の要件](agents/common.md): 結果の返し方、指示の渡し方、GitHub上の身元、プロセスとセッション
- [Plannerの要件](agents/planner.md): 要求Issueを実装Issueに分割し、依存関係とriskを付ける
- [Implementerの要件](agents/implementer.md): 実装Issueを実装して、Pull Requestにまとめる
- [Reviewerの要件](agents/reviewer.md): Pull Requestをレビューし、指摘が残ったら原因を整理する

## Agentが従う判断基準

- [要求Issueの分割基準](policies/requirement-sizing.md): Ownerが要求Issueを書くときの、1つの要求Issueの大きさの基準
- [実装Issueの分割基準](policies/issue-sizing.md): Plannerが要求Issueを分割するときの基準
- [GitHubに残す文章のテンプレート](policies/writing-templates.md): 要求Issue、実装Issue、Pull Requestの説明、レビュー、返答、Ownerに判断を求める文章の型

## 調査と実測

- [調査・実測で確定した制約](evidence/measured-constraints.md): Claude Codeの利用枠、GitHub上の身元について調べた事実
