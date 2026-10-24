// Building blocks the admin screens share.
import { ActionIcon, Alert, Badge, Code, CopyButton, Group, Paper, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { IconCheck, IconCopy, IconInfoCircle } from "@tabler/icons-react";

/** A screen's heading, with a line saying what it is for. */
export function Screen({ title, about, actions, children }: { title: string; about?: string; actions?: React.ReactNode; children: React.ReactNode }) {
  return (
    <Stack gap="lg">
      <Group justify="space-between" align="flex-end">
        <div>
          <Title order={1} size="h2">
            {title}
          </Title>
          {about && (
            <Text c="dimmed" fz="sm">
              {about}
            </Text>
          )}
        </div>
        {actions}
      </Group>
      {children}
    </Stack>
  );
}

/** A table in a card, scrolling sideways when the screen is narrow. */
export function ListTable({ minWidth = 640, children, ...rest }: { minWidth?: number; children: React.ReactNode } & React.ComponentProps<typeof Table>) {
  return (
    <Paper>
      <Table.ScrollContainer minWidth={minWidth} type="native">
        <Table {...rest}>{children}</Table>
      </Table.ScrollContainer>
    </Paper>
  );
}

const statusColors: Record<string, string> = {
  active: "green",
  open: "blue",
  up: "green",
  used: "gray",
  expired: "gray",
  withdrawn: "gray",
  revoked: "gray",
  suspended: "orange",
  down: "red",
};

/** A status as a coloured badge; the colour follows its first word. */
export function Status({ children }: { children: string }) {
  const color = statusColors[children.split(/[ ,]/)[0].toLowerCase()] ?? "gray";
  return (
    <Badge className="status" color={color} variant="light" tt="none">
      {children}
    </Badge>
  );
}

/** A secret or command shown once, with a copy button. */
export function Shown({ label, value, testid, block }: { label: React.ReactNode; value: string; testid: string; block?: boolean }) {
  return (
    <Alert color="indigo" variant="light" icon={<IconInfoCircle />}>
      <Stack gap="xs">
        <Text fz="sm">{label}</Text>
        <Group gap="xs" wrap="nowrap" align="flex-start">
          <Code block={block} data-testid={testid} style={{ flex: 1 }}>
            {value}
          </Code>
          <CopyButton value={value}>
            {({ copied, copy }) => (
              <Tooltip label={copied ? "Copied" : "Copy"} withArrow>
                <ActionIcon variant="subtle" color={copied ? "teal" : "gray"} onClick={copy} aria-label="Copy">
                  {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                </ActionIcon>
              </Tooltip>
            )}
          </CopyButton>
        </Group>
      </Stack>
    </Alert>
  );
}

/** An error from the API, if any. */
export function Problem({ children }: { children?: string }) {
  if (!children) return null;
  return (
    <Text c="red" fz="sm">
      {children}
    </Text>
  );
}
