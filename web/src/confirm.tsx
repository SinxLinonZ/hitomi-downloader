import { ReactNode, useCallback, useState } from "react";
import { Button, Group, Modal, Text } from "@mantine/core";
import { t } from "./i18n";

export type ConfirmOptions = {
  title: string;
  // 何が起きるか・何は起きないかを書く。
  message: ReactNode;
  confirmLabel: string;
  color?: string;
};

// 一括操作の誤クリックを防ぐ確認ダイアログ。confirm() は押された結果を boolean で返す。
// 既定のフォーカスは「取消」に置き、Enter の連打で実行されないようにする。
export function useConfirm() {
  const [state, setState] = useState<
    (ConfirmOptions & { resolve: (ok: boolean) => void }) | null
  >(null);
  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => setState({ ...options, resolve })),
    [],
  );
  const finish = (ok: boolean) => {
    state?.resolve(ok);
    setState(null);
  };
  const element = (
    <Modal
      opened={!!state}
      onClose={() => finish(false)}
      title={state?.title}
      centered
      size="sm"
    >
      <Text size="sm" component="div">
        {state?.message}
      </Text>
      <Group justify="flex-end" mt="md" gap="xs">
        <Button variant="default" data-autofocus onClick={() => finish(false)}>
          {t("取消")}
        </Button>
        <Button color={state?.color} onClick={() => finish(true)}>
          {state?.confirmLabel}
        </Button>
      </Group>
    </Modal>
  );
  return { confirm, element };
}
