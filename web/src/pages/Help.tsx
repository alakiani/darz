import { Accordion, List, Paper, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import { IconBrandAndroid, IconBrandApple, IconBrandFinder, IconBrandWindows, IconKey, IconPlugConnected } from "@tabler/icons-react";
import { fa } from "../fa";

// The per-platform help page from the panel prototype (issue #14): install
// WireGuard, import the config, and toggle the tunnel when stuck (ADR 0011).
const platforms = [
  {
    name: "اندروید",
    icon: IconBrandAndroid,
    steps: [
      "برنامهٔ WireGuard را از Google Play یا F-Droid نصب کنید. اگر در دسترس نبود، فایل نصبی را از کسی که به او اعتماد دارید بگیرید.",
      "در برنامه دکمهٔ + را بزنید و «اسکن از کد QR» را انتخاب کنید، یا «وارد کردن از فایل» و فایل ways.conf را بدهید.",
    ],
  },
  {
    name: "آیفون و آیپد",
    icon: IconBrandApple,
    steps: [
      "برنامهٔ WireGuard را از App Store نصب کنید.",
      "«Add a tunnel» و سپس «Create from QR code» را بزنید و کد پیکربندی را اسکن کنید.",
      "اجازهٔ افزودن پیکربندی VPN را بدهید.",
    ],
  },
  {
    name: "ویندوز",
    icon: IconBrandWindows,
    steps: [
      "برنامهٔ WireGuard را از wireguard.com نصب کنید.",
      "«Import tunnel(s) from file» را بزنید و فایل ways.conf را انتخاب کنید.",
    ],
  },
  {
    name: "مک",
    icon: IconBrandFinder,
    steps: [
      "برنامهٔ WireGuard را از Mac App Store نصب کنید.",
      "«Import Tunnel(s) from File» را بزنید و فایل ways.conf را انتخاب کنید.",
    ],
  },
];

export function HelpPage() {
  return (
    <Stack gap="lg">
      <Title order={1} size="h2">
        {fa.help}
      </Title>
      <Accordion variant="separated" radius="lg" multiple>
        {platforms.map((p) => (
          <Accordion.Item key={p.name} value={p.name}>
            <Accordion.Control
              icon={
                <ThemeIcon variant="light" radius="md">
                  <p.icon size={18} />
                </ThemeIcon>
              }
            >
              <Title order={2} size="h5">
                {p.name}
              </Title>
            </Accordion.Control>
            <Accordion.Panel>
              <List type="ordered" spacing="xs">
                {p.steps.map((s) => (
                  <List.Item key={s}>{s}</List.Item>
                ))}
              </List>
            </Accordion.Panel>
          </Accordion.Item>
        ))}
      </Accordion>
      <Tip icon={<IconPlugConnected size={18} />} title="اگر وصل است ولی چیزی باز نمی‌شود">
        {fa.stuckHint}
      </Tip>
      <Tip icon={<IconKey size={18} />} title="اگر پیکربندی گم شد">
        پیکربندی را نمی‌توان دوباره دریافت کرد، چون کلید خصوصی فقط روی دستگاه شماست. در صفحهٔ دستگاه، «جایگزینی» را بزنید تا
        پیکربندی تازه بسازید.
      </Tip>
    </Stack>
  );
}

function Tip({ icon, title, children }: { icon: React.ReactNode; title: string; children: React.ReactNode }) {
  return (
    <Paper p="lg">
      <Stack gap="xs">
        <Title order={2} size="h5">
          <ThemeIcon variant="light" color="gray" radius="md" me="xs">
            {icon}
          </ThemeIcon>
          {title}
        </Title>
        <Text fz="sm" c="dimmed">
          {children}
        </Text>
      </Stack>
    </Paper>
  );
}
