import { useEffect, useState } from "react";
import { Button, Group, Paper, Table, TextInput } from "@mantine/core";
import { IconKey } from "@tabler/icons-react";
import { api } from "../api/client";
import type { TokenInfo } from "./api";
import { ListTable, Screen, Shown, Status } from "./ui";

// API tokens: nodes:enroll only, for the operator's laptop (ADR 0002).
export function TokensScreen() {
  const [tokens, setTokens] = useState<TokenInfo[]>([]);
  const [name, setName] = useState("");
  const [secret, setSecret] = useState("");
  const load = () => api.GET("/v1/admin/tokens").then(({ data }) => setTokens(data ?? []));
  useEffect(() => {
    load();
  }, []);

  return (
    <Screen title="API tokens" about="For enrolling Nodes from the operator's laptop.">
      <Paper
        component="form"
        p="lg"
        onSubmit={async (e) => {
          e.preventDefault();
          const { data } = await api.POST("/v1/admin/tokens", { body: { name } });
          if (data) setSecret(data.secret);
          setName("");
          load();
        }}
      >
        <Group align="flex-end">
          <TextInput label="Name" name="name" required value={name} onChange={(e) => setName(e.target.value)} />
          <Button type="submit" leftSection={<IconKey size={16} />}>
            Make a nodes:enroll token
          </Button>
        </Group>
      </Paper>
      {secret && <Shown label="Shown once; keep it in ansible-vault or the keychain. It lasts 90 days:" value={secret} testid="token-secret" />}
      <ListTable>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Name</Table.Th>
            <Table.Th>Scope</Table.Th>
            <Table.Th>Expires</Table.Th>
            <Table.Th>Status</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {tokens.map((t) => (
            <Table.Tr key={t.id} data-testid={`token-${t.name}`}>
              <Table.Td fw={600}>{t.name}</Table.Td>
              <Table.Td>{t.scope}</Table.Td>
              <Table.Td>{new Date(t.expires_at).toLocaleDateString()}</Table.Td>
              <Table.Td>
                <Status>{t.revoked ? "revoked" : "active"}</Status>
              </Table.Td>
              <Table.Td>
                {!t.revoked && (
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="red"
                    onClick={async () => {
                      await api.POST("/v1/admin/tokens/{id}/revoke", { params: { path: { id: t.id } } });
                      load();
                    }}
                  >
                    Revoke
                  </Button>
                )}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </ListTable>
    </Screen>
  );
}
