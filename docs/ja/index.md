# cumin works v2 ドキュメント

## 要求と要件

- [要求と要件の入口](requirements/index.md)
- [要求仕様書](requirements/overview.md)

## 調査と実測

- [調査・実測で確定した制約](evidence/measured-constraints.md): 公式文書と実機で確かめた、外部の道具 (Claude Code、GitHub、git、macOS、Discord) の振る舞い。要件ではなく、事実の記録

## 設計

- [コードの構成の設計](designs/code-layout.md)
- [cumin本体の設計メモ](designs/cumin-core.md)
- [定期確認の設計](designs/poll.md)
- [Agentの実行の設計](designs/agent-run.md)
- [利用枠の設計](designs/quota.md)
- [モニターファイルとメニューバーのアプリの設計](designs/status-menu-bar.md)

## ガイド

- [既存のプロダクトにcuminを入れる](guides/adoption.md): 入れる前とあとで決めること
- [メニューバーのアプリを作って起動する](guides/status-menu-bar.md): ビルド、起動、ログインのときの起動、メニューの見方、設定
- [Hostの一般の道具](guides/host-tools.md): 端末から使う `scripts/` の道具。cuminが動いているかを確かめる `cumin-health.sh`、バイナリを入れ替える `install.sh --after-current-runs`、実機の場面 E2E-1 を実行する `live-scenario.sh`
- [Maintainerのセッションにskillを入れる](guides/maintainer-skills.md): cuminと並んで働くClaude Codeのセッションのplugin。インストール、scope、更新、skillごとの使い方、権限のルールの一覧

## 開発の手順

- [はじめに](getting-started.md)
- [設定の一覧](development/configuration.md)
- [作業場所の片付け](development/work-directory.md)
- [セットアップの手順](development/setup-guide.md)
- [GitHub Appの登録手順 (手作業)](development/github-app-setup.md)
- [実機の確認 (live test)](development/live-tests.md)
- [図の描き方](development/diagrams.md)
- [Agentの実機の確認](development/agent-live-check.md)
- [launchdでの実機の確認](development/launchd-live-check.md)
- [設計文書の書き方](development/design-documents.md)
