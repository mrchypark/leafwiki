import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test, { Browser, Page, expect } from '@playwright/test';
import { getCsrfScript } from '../helpers/api';
import AddPageDialog from '../pages/AddPageDialog';
import LoginPage from '../pages/LoginPage';
import NotFoundPage from '../pages/NotFoundPage';
import TreeView from '../pages/TreeView';
import ViewPage from '../pages/ViewPage';
import { toAppPath } from '../pages/appPath';

const user = process.env.E2E_ADMIN_USER || 'admin';
const password = process.env.E2E_ADMIN_PASSWORD || 'admin';
const baseURL = process.env.E2E_BASE_URL || 'http://localhost:8080';
const publicAccess = process.env.E2E_PUBLIC_ACCESS === 'true';
const execFileAsync = promisify(execFile);

type ApiPage = {
  id: string;
  title: string;
  slug: string;
  path: string;
  version: string;
  kind: 'page' | 'section';
  draft?: boolean;
  effectiveDraft?: boolean;
};

async function login(page: Page) {
  const loginPage = new LoginPage(page);
  await loginPage.goto();
  await loginPage.login(user, password);
  await new ViewPage(page).expectUserLoggedIn();
}

async function mutate<T>(
  page: Page,
  path: string,
  method: 'POST' | 'PUT',
  body: Record<string, unknown>,
): Promise<T> {
  return page.evaluate(
    async ({ path, method, body, csrfScript }) => {
      const csrfToken = new Function(csrfScript)() as string;
      const response = await fetch(path, {
        method,
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': csrfToken,
        },
        body: JSON.stringify(body),
      });
      if (!response.ok) {
        throw new Error(`${method} ${path} failed: ${response.status} ${await response.text()}`);
      }
      return (await response.json()) as T;
    },
    { path: toAppPath(path), method, body, csrfScript: getCsrfScript() },
  );
}

async function createNode(
  page: Page,
  input: {
    title: string;
    slug: string;
    parentId?: string | null;
    kind?: 'page' | 'section';
    draft?: boolean;
  },
): Promise<ApiPage> {
  return mutate<ApiPage>(page, '/api/pages', 'POST', {
    parentId: input.parentId ?? null,
    title: input.title,
    slug: input.slug,
    kind: input.kind ?? 'page',
    draft: input.draft ?? false,
  });
}

async function getPageByPath(page: Page, path: string): Promise<ApiPage> {
  return page.evaluate(
    async (apiPath) => {
      const response = await fetch(apiPath, { credentials: 'include' });
      if (!response.ok) {
        throw new Error(`GET ${apiPath} failed: ${response.status} ${await response.text()}`);
      }
      return (await response.json()) as ApiPage;
    },
    toAppPath(`/api/pages/by-path?path=${encodeURIComponent(path)}`),
  );
}

async function setDraft(page: Page, current: ApiPage, draft: boolean): Promise<ApiPage> {
  return mutate<ApiPage>(page, `/api/pages/${current.id}/draft`, 'PUT', {
    version: current.version,
    draft,
  });
}

async function movePage(page: Page, current: ApiPage, parentId: string): Promise<void> {
  await mutate<{ message: string }>(page, `/api/pages/${current.id}/move`, 'PUT', {
    version: current.version,
    parentId,
  });
}

function expectDraftState(page: ApiPage, draft: boolean, effectiveDraft: boolean) {
  expect(Boolean(page.draft)).toBe(draft);
  expect(Boolean(page.effectiveDraft)).toBe(effectiveDraft);
}

async function newAnonymousContext(browser: Browser) {
  return browser.newContext({ baseURL });
}

test.describe('inherited draft subtrees', () => {
  test.skip(!publicAccess, 'Run with E2E_PUBLIC_ACCESS=true to verify anonymous draft visibility.');

  test('creates a child below a draft parent and labels it as inherited', async ({ page }) => {
    await login(page);
    const stamp = Date.now();
    const parent = await createNode(page, {
      title: `Draft parent ${stamp}`,
      slug: `draft-parent-${stamp}`,
      draft: true,
    });
    const childTitle = `Inherited child ${stamp}`;
    const childSlug = `inherited-child-${stamp}`;

    await page.reload();
    const parentRow = page.getByTestId(`tree-node-${parent.id}`);
    await parentRow.hover();
    const addButton = parentRow.getByTestId('tree-view-action-button-add');
    await expect(addButton).toBeVisible({ timeout: 5_000 });
    await addButton.click();

    const addDialog = new AddPageDialog(page);
    await addDialog.fillTitle(childTitle);
    await expect(page.getByText('Inherited draft', { exact: true })).toBeVisible();
    await expect(
      page.getByRole('checkbox', { name: 'Keep draft when parent is published' }),
    ).not.toBeChecked();
    await addDialog.submitWithoutRedirect();

    const child = await getPageByPath(page, `${parent.slug}/${childSlug}`);
    expectDraftState(child, false, true);

    const tree = new TreeView(page);
    await tree.expandNodeByTitle(parent.title);
    await expect(
      page.getByTestId(`tree-node-${child.id}`).getByText('Inherited draft', { exact: true }),
    ).toBeVisible();
  });

  test('denies anonymous access to both a draft parent and its child', async ({
    page,
    browser,
  }) => {
    await login(page);
    const stamp = Date.now();
    const parent = await createNode(page, {
      title: `Private section ${stamp}`,
      slug: `private-section-${stamp}`,
      kind: 'section',
      draft: true,
    });
    const child = await createNode(page, {
      title: `Private child ${stamp}`,
      slug: `private-child-${stamp}`,
      parentId: parent.id,
    });
    const childPath = `${parent.slug}/${child.slug}`;

    const anonymous = await newAnonymousContext(browser);
    try {
      for (const path of [parent.slug, childPath]) {
        const response = await anonymous.request.get(
          toAppPath(`/api/pages/by-path?path=${encodeURIComponent(path)}`),
        );
        expect(response.status()).toBe(404);

        const anonymousPage = await anonymous.newPage();
        await anonymousPage.goto(toAppPath(`/${path}`));
        await new NotFoundPage(anonymousPage).expectVisible();
        await anonymousPage.close();
      }
    } finally {
      await anonymous.close();
    }
  });

  test('publishing a parent reveals a normal child but keeps an own-draft child private', async ({
    page,
    browser,
  }) => {
    await login(page);
    const stamp = Date.now();
    const parent = await createNode(page, {
      title: `Publishing section ${stamp}`,
      slug: `publishing-section-${stamp}`,
      kind: 'section',
      draft: true,
    });
    const normalChild = await createNode(page, {
      title: `Normal child ${stamp}`,
      slug: `normal-child-${stamp}`,
      parentId: parent.id,
    });
    const ownDraftChild = await createNode(page, {
      title: `Own draft child ${stamp}`,
      slug: `own-draft-child-${stamp}`,
      parentId: parent.id,
      draft: true,
    });
    const normalPath = `${parent.slug}/${normalChild.slug}`;
    const ownDraftPath = `${parent.slug}/${ownDraftChild.slug}`;

    expectDraftState(await getPageByPath(page, normalPath), false, true);
    expectDraftState(await getPageByPath(page, ownDraftPath), true, true);
    await setDraft(page, await getPageByPath(page, parent.slug), false);

    expectDraftState(await getPageByPath(page, normalPath), false, false);
    expectDraftState(await getPageByPath(page, ownDraftPath), true, true);

    const anonymous = await newAnonymousContext(browser);
    try {
      const publishedResponse = await anonymous.request.get(
        toAppPath(`/api/pages/by-path?path=${encodeURIComponent(normalPath)}`),
      );
      expect(publishedResponse.status()).toBe(200);
      const stillDraftResponse = await anonymous.request.get(
        toAppPath(`/api/pages/by-path?path=${encodeURIComponent(ownDraftPath)}`),
      );
      expect(stillDraftResponse.status()).toBe(404);

      const anonymousPage = await anonymous.newPage();
      await anonymousPage.goto(toAppPath(`/${normalPath}`));
      await expect(anonymousPage.locator('article>h1')).toHaveText(normalChild.title);
      await anonymousPage.goto(toAppPath(`/${ownDraftPath}`));
      await new NotFoundPage(anonymousPage).expectVisible();
    } finally {
      await anonymous.close();
    }
  });

  test('moving an inherited draft child to a public section keeps it private', async ({
    page,
    browser,
  }) => {
    await login(page);
    const stamp = Date.now();
    const source = await createNode(page, {
      title: `Move source ${stamp}`,
      slug: `move-source-${stamp}`,
      kind: 'section',
      draft: true,
    });
    const destination = await createNode(page, {
      title: `Move destination ${stamp}`,
      slug: `move-destination-${stamp}`,
      kind: 'section',
    });
    const child = await createNode(page, {
      title: `Moved child ${stamp}`,
      slug: `moved-child-${stamp}`,
      parentId: source.id,
    });

    expectDraftState(await getPageByPath(page, `${source.slug}/${child.slug}`), false, true);
    await movePage(page, await getPageByPath(page, `${source.slug}/${child.slug}`), destination.id);

    const movedPath = `${destination.slug}/${child.slug}`;
    const moved = await getPageByPath(page, movedPath);
    expectDraftState(moved, true, true);

    const anonymous = await newAnonymousContext(browser);
    try {
      const response = await anonymous.request.get(
        toAppPath(`/api/pages/by-path?path=${encodeURIComponent(movedPath)}`),
      );
      expect(response.status()).toBe(404);
    } finally {
      await anonymous.close();
    }

    await page.goto(toAppPath(`/${movedPath}`));
    await expect(page.locator('article>h1')).toHaveText(child.title);
    await expect(page.locator('.page-viewer__subheader .draft-badge')).toHaveText('Draft');
  });

  test('preserves section and inherited draft state after a server restart', async ({ page }) => {
    const containerName = process.env.E2E_CONTAINER_NAME;
    test.skip(
      !containerName,
      'A real restart requires the default Docker E2E harness; local mode has no container.',
    );

    await login(page);
    const stamp = Date.now();
    const parent = await createNode(page, {
      title: `Restart section ${stamp}`,
      slug: `restart-section-${stamp}`,
      kind: 'section',
      draft: true,
    });
    const child = await createNode(page, {
      title: `Restart child ${stamp}`,
      slug: `restart-child-${stamp}`,
      parentId: parent.id,
    });
    const childPath = `${parent.slug}/${child.slug}`;

    expectDraftState(await getPageByPath(page, parent.slug), true, true);
    expectDraftState(await getPageByPath(page, childPath), false, true);

    await execFileAsync('docker', ['restart', containerName!]);
    await expect
      .poll(
        async () => {
          try {
            const response = await page.request.get(toAppPath('/api/health'));
            if (response.status() !== 200) return `status:${response.status()}`;
            const health = (await response.json()) as {
              status: string;
              checks: Record<string, string>;
            };
            return `${health.status}:${health.checks.search}`;
          } catch {
            return 'unreachable';
          }
        },
        { timeout: 60_000 },
      )
      .toBe('ok:ok');

    expectDraftState(await getPageByPath(page, parent.slug), true, true);
    expectDraftState(await getPageByPath(page, childPath), false, true);

    await page.goto(toAppPath(`/${childPath}`));
    await expect(page.locator('article>h1')).toHaveText(child.title);
    await expect(page.locator('.page-viewer__subheader .draft-badge')).toHaveText(
      'Inherited draft',
    );
  });
});
