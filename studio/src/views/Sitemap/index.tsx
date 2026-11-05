import { Link } from "wouter";

import { useMaybeAuth } from "@/auth/AuthContext";
import { NAV_REGISTRY } from "@/components/shared/NavLinks";
import { isNavHrefAvailable } from "@/components/shared/navAvailability";
import { useServerInfoStore } from "@/store/serverInfo";
import { ADMIN_BASE, ADMIN_SECTIONS } from "@/views/admin/adminRoutes";
import { LegalSitemapGroup } from "@/views/Legal/LegalSidebarLinks";

// The sitemap page (RGAA 12.1): a second navigation system generated from the
// SAME link registry the sidebar renders — plus the admin console sections.
// It lists only what this server answers: pages gated by a server_info flag
// are dropped when the flag is off, and the admin console (a cloud-only API)
// appears in cloud mode for a super-admin only. Coverage is pinned by
// Sitemap.test.tsx against App.tsx's route literals.
export default function Sitemap() {
  const info = useServerInfoStore((s) => s.info);
  const auth = useMaybeAuth();
  const studioGroups = NAV_REGISTRY.map((g) => ({
    ...g,
    hrefs: g.hrefs.filter((h) => isNavHrefAvailable(h.href, info)),
  })).filter((g) => g.hrefs.length > 0);
  const showAdmin =
    info?.mode === "cloud" && !!auth?.user?.is_super_admin;
  const groups = [
    { heading: "Studio", groups: studioGroups },
    ...(showAdmin
      ? [
          {
            heading: "Admin console",
            groups: ADMIN_SECTIONS.map((s) => ({
              label: s.label as string | null,
              hrefs: s.items.map((i) => ({
                href: `${ADMIN_BASE}${i.path}`,
                label: i.label,
              })),
            })),
          },
        ]
      : []),
  ];
  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="max-w-3xl">
        <h1 className="text-title font-semibold text-fg-default mb-4">
          Sitemap
        </h1>
        {groups.map((g) => (
          <section key={g.heading} className="mb-6">
            <h2 className="text-body font-semibold text-fg-default mb-2">
              {g.heading}
            </h2>
            {g.groups.map((group, i) => (
              <div key={i} className="mb-3">
                {/* Groups that only repeat their single link's name get no
                    caption; the admin sections do carry one. */}
                {group.label &&
                  !(
                    group.hrefs.length === 1 &&
                    group.hrefs[0]!.label === group.label
                  ) && (
                    <div className="text-caption font-semibold uppercase tracking-wider text-fg-subtle mb-1">
                      {group.label}
                    </div>
                  )}
                <ul className="ml-4 list-disc space-y-0.5">
                  {group.hrefs.map(({ href, label }) => (
                    <li key={href}>
                      <Link href={href} className="text-accent-text hover:underline">
                        {label}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </section>
        ))}
        {/* The legal pages are configured per deployment, so their group is
            read from the server rather than the static registry. */}
        <LegalSitemapGroup />
      </div>
    </div>
  );
}
