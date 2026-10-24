import { useEffect, useState } from "react";
import { Anchor, Badge, Button, Checkbox, Group, Paper, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { IconArrowLeft, IconPlus, IconTrash } from "@tabler/icons-react";
import { api } from "../api/client";
import { go } from "../router";
import { ago, typedConfirm, type NodeDetail, type NodeTile } from "./api";
import { ListTable, Problem, Screen, Shown, Status } from "./ui";

type Role = "bridge" | "exit";
const roleList: Role[] = ["bridge", "exit"];
const splitTags = (s: string) =>
  s
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter(Boolean);

function RoleBoxes({ roles, setRoles }: { roles: Role[]; setRoles: (r: Role[]) => void }) {
  return (
    <Group gap="md">
      {roleList.map((r) => (
        <Checkbox
          key={r}
          name={`role-${r}`}
          label={r}
          checked={roles.includes(r)}
          onChange={(e) => setRoles(e.target.checked ? [...roles, r] : roles.filter((x) => x !== r))}
        />
      ))}
    </Group>
  );
}

function Roles({ roles }: { roles: string[] }) {
  return (
    <Group gap={4}>
      {roles.map((r) => (
        <Badge key={r} size="sm" variant="light" color={r === "bridge" ? "violet" : "cyan"}>
          {r}
        </Badge>
      ))}
    </Group>
  );
}

// Nodes: add with the Ansible command, open, edit Roles and Tags, delete.
export function NodesScreen() {
  const [nodes, setNodes] = useState<NodeTile[]>([]);
  const [name, setName] = useState("");
  const [roles, setRoles] = useState<Role[]>(["exit"]);
  const [tags, setTags] = useState("");
  const [host, setHost] = useState("");
  const [command, setCommand] = useState("");
  const [error, setError] = useState("");
  const load = () => api.GET("/v1/admin/nodes").then(({ data }) => setNodes(data ?? []));
  useEffect(() => {
    load();
  }, []);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const { data, error } = await api.POST("/v1/admin/nodes", { body: { name, roles, tags: splitTags(tags), ssh_host: host } });
    if (!data) return setError((error as { error?: string })?.error ?? "failed");
    setCommand(data.command ?? "Already enrolled; nothing to run.");
    load();
  };

  return (
    <Screen title="Nodes" about="Bridges inside Iran and Exits abroad.">
      <ListTable>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Name</Table.Th>
            <Table.Th>Roles</Table.Th>
            <Table.Th>Tags</Table.Th>
            <Table.Th>Contact</Table.Th>
            <Table.Th>Snapshot</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {nodes.map((n) => (
            <Table.Tr key={n.id} data-testid={`node-row-${n.name}`}>
              <Table.Td>
                <Anchor href={`#/admin/nodes/${n.id}`} fw={600} fz="sm">
                  {n.name}
                </Anchor>
              </Table.Td>
              <Table.Td>
                <Roles roles={n.roles} />
              </Table.Td>
              <Table.Td>{n.tags.join(", ")}</Table.Td>
              <Table.Td c={n.enrolled ? undefined : "dimmed"}>{n.enrolled ? ago(n.last_contact) : "not enrolled"}</Table.Td>
              <Table.Td>
                {n.applied_version}/{n.latest_version}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </ListTable>

      <Paper component="form" p="lg" onSubmit={add}>
        <Stack>
          <Title order={2} size="h5">
            Add a Node
          </Title>
          <Group align="flex-end">
            <TextInput label="Name" name="name" required value={name} onChange={(e) => setName(e.target.value)} />
            <TextInput label="Tags" placeholder="such as IR" name="tags" value={tags} onChange={(e) => setTags(e.target.value)} />
            <TextInput label="SSH host" description="for the command only" name="host" required value={host} onChange={(e) => setHost(e.target.value)} />
          </Group>
          <RoleBoxes roles={roles} setRoles={setRoles} />
          <Problem>{error}</Problem>
          <Group>
            <Button type="submit" leftSection={<IconPlus size={16} />}>
              Add Node
            </Button>
          </Group>
        </Stack>
      </Paper>
      {command && (
        <Shown label="Run this from the operator's laptop within the hour; the token works once:" value={command} testid="enroll-command" block />
      )}
    </Screen>
  );
}

export function NodeScreen({ id }: { id: string }) {
  const [d, setD] = useState<NodeDetail | null>(null);
  const [roles, setRoles] = useState<Role[]>([]);
  const [tags, setTags] = useState("");
  const load = (form: boolean) =>
    api.GET("/v1/admin/nodes/{id}", { params: { path: { id } } }).then(({ data }) => {
      setD(data ?? null);
      if (data && form) {
        setRoles(data.node.roles as Role[]);
        setTags(data.node.tags.join(", "));
      }
    });
  useEffect(() => {
    load(true);
    // Contact, Links and Paths change by the second; the edit form does not.
    const t = setInterval(() => load(false), 5000);
    return () => clearInterval(t);
  }, [id]);
  if (!d) return null;
  const n = d.node;

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    await api.POST("/v1/admin/nodes/{id}", { params: { path: { id } }, body: { roles, tags: splitTags(tags) } });
    load(true);
  };
  const remove = async () => {
    if (!typedConfirm("delete this Node; its certificate stops working at once", n.name)) return;
    await api.DELETE("/v1/admin/nodes/{id}", { params: { path: { id } } });
    go("/admin/nodes");
  };

  return (
    <Stack gap="lg">
      <Anchor href="#/admin/nodes" fz="sm">
        <Group gap={4}>
          <IconArrowLeft size={14} />
          All Nodes
        </Group>
      </Anchor>
      <div>
        <Group gap="sm">
          <Title order={1} size="h2" data-testid="node-name">
            {n.name}
          </Title>
          <Roles roles={n.roles} />
        </Group>
        <Text c="dimmed" fz="sm">
          {n.enrolled ? `contact ${ago(n.last_contact)}` : "not enrolled"} · snapshot {n.applied_version}/{n.latest_version}
          {n.agent_version && ` · agent ${n.agent_version}`}
        </Text>
      </div>
      <Paper component="form" p="lg" onSubmit={save}>
        <Group align="flex-end">
          <TextInput label="Tags" name="tags" value={tags} onChange={(e) => setTags(e.target.value)} />
          <Stack gap={6} pb={8}>
            <Text fz="sm" fw={500}>
              Roles
            </Text>
            <RoleBoxes roles={roles} setRoles={setRoles} />
          </Stack>
          <Button type="submit">Save</Button>
        </Group>
      </Paper>

      {d.paths.length > 0 && (
        <>
          <Title order={2} size="h4">
            Paths
          </Title>
          <ListTable data-testid="paths">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Rule</Table.Th>
                <Table.Th>Mode</Table.Th>
                <Table.Th>Eligible</Table.Th>
                <Table.Th>Buckets</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {d.paths.map((p) => (
                <Table.Tr key={p.rule}>
                  <Table.Td fw={600}>{p.rule}</Table.Td>
                  <Table.Td>{p.mode}</Table.Td>
                  <Table.Td>{p.eligible}</Table.Td>
                  <Table.Td>{p.shares.map((s) => `${s.candidate}${s.transit ? " (transit)" : ""}: ${s.buckets}`).join(", ")}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </ListTable>
        </>
      )}

      <Title order={2} size="h4">
        Links
      </Title>
      <ListTable minWidth={360}>
        <Table.Tbody>
          {d.links.map((l) => (
            <Table.Tr key={l.id}>
              <Table.Td fw={600}>
                {l.a} – {l.b}
              </Table.Td>
              <Table.Td>
                <Status>{l.up_a || l.up_b ? `up, ${l.transport}, ${l.rtt_ms.toFixed(0)} ms` : "down"}</Status>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </ListTable>

      <Paper p="lg" style={{ borderColor: "var(--mantine-color-red-light-color)" }}>
        <Group justify="space-between">
          <div>
            <Text fw={600}>Delete this Node</Text>
            <Text c="dimmed" fz="sm">
              Its certificate stops working at once.
            </Text>
          </div>
          <Button color="red" variant="light" leftSection={<IconTrash size={16} />} onClick={remove}>
            Delete Node
          </Button>
        </Group>
      </Paper>
    </Stack>
  );
}
