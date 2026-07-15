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

type ApiUser = {
  id: string;
  username: string;
  email: string;
  role: 'admin' | 'editor' | 'viewer';
};

async function loginAs(page: Page, username: string, loginPassword: string) {
  const loginPage = new LoginPage(page);
  await loginPage.goto();
  await loginPage.login(username, loginPassword);
  await new ViewPage(page).expectUserLoggedIn();
}

async function login(page: Page) {
  await loginAs(page, user, password);
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

async function createUser(
  page: Page,
  input: { username: string; email: string; password: string; role: ApiUser['role'] },
): Promise<ApiUser> {
  return mutate<ApiUser>(page, '/api/users', 'POST', input);
}

async function requestStatus(page: Page, path: string, method: 'PUT' | 'DELETE'): Promise<number> {
  return page.evaluate(
    async ({ path, method, csrfScript }) => {
      const csrfToken = new Function(csrfScript)() as string;
      const response = await fetch(path, {
        method,
        credentials: 'include',
        headers: { 'X-CSRF-Token': csrfToken },
      });
      return response.status;
    },
    { path: toAppPath(path), method, csrfScript: getCsrfScript() },
  );
}

async function getFavoritePageIds(page: Page): Promise<string[]> {
  return page.evaluate(async (apiPath) => {
    const response = await fetch(apiPath, { credentials: 'include' });
    if (!response.ok) {
      throw new Error(`GET ${apiPath} failed: ${response.status} ${await response.text()}`);
    }
    const result = (await response.json()) as { pages: ApiPage[] };
    return result.pages.map((favorite) => favorite.id);
  }, toAppPath('/api/favorites'));
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

async function dragNodeBefore(page: Page, draggedId: string, targetId: string): Promise<void> {
  const dragged = page.getByTestId(`tree-node-${draggedId}`);
  const target = page.getByTestId(`tree-node-${targetId}`);
  await dragged.scrollIntoViewIfNeeded();
  await target.scrollIntoViewIfNeeded();

  const draggedBox = await dragged.boundingBox();
  const targetBox = await target.boundingBox();
  if (!draggedBox || !targetBox) {
    throw new Error('Expected draggable tree rows to have bounding boxes');
  }

  const moveResponse = page.waitForResponse(
    (response) =>
      response.request().method() === 'PUT' &&
      response.url().includes(`/api/pages/${draggedId}/move`),
  );

  await page.mouse.move(draggedBox.x + draggedBox.width / 2, draggedBox.y + draggedBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(
    draggedBox.x + draggedBox.width / 2 + 10,
    draggedBox.y + draggedBox.height / 2 + 10,
    { steps: 4 },
  );
  await page.mouse.move(targetBox.x + targetBox.width / 2, targetBox.y + 2, { steps: 20 });
  await expect(target.locator('.tree-node__drop-line--top')).toBeVisible();
  await page.mouse.up();

  expect((await moveResponse).ok()).toBe(true);
}

async function ensureSidebarOpen(page: Page): Promise<void> {
  const toggle = page.getByTestId('sidebar-toggle-button');
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') {
    await toggle.click();
  }
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
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

  test('viewer favorites never expose direct, inherited, or newly hidden drafts', async ({
    page,
    browser,
  }) => {
    await login(page);
    const stamp = Date.now();
    const directDraft = await createNode(page, {
      title: `Favorite direct draft ${stamp}`,
      slug: `favorite-direct-draft-${stamp}`,
      draft: true,
    });
    const draftParent = await createNode(page, {
      title: `Favorite draft parent ${stamp}`,
      slug: `favorite-draft-parent-${stamp}`,
      kind: 'section',
      draft: true,
    });
    const inheritedDraft = await createNode(page, {
      title: `Favorite inherited draft ${stamp}`,
      slug: `favorite-inherited-draft-${stamp}`,
      parentId: draftParent.id,
    });
    const published = await createNode(page, {
      title: `Favorite published page ${stamp}`,
      slug: `favorite-published-page-${stamp}`,
    });
    const viewerUsername = `draft-viewer-${stamp}`;
    const viewerPassword = `viewer-password-${stamp}`;
    await createUser(page, {
      username: viewerUsername,
      email: `${viewerUsername}@example.com`,
      password: viewerPassword,
      role: 'viewer',
    });

    expect(await requestStatus(page, `/api/pages/${directDraft.id}/favorite`, 'PUT')).toBe(200);
    await expect.poll(() => getFavoritePageIds(page)).toContain(directDraft.id);

    const viewerContext = await browser.newContext({ baseURL });
    const viewerPage = await viewerContext.newPage();
    try {
      await loginAs(viewerPage, viewerUsername, viewerPassword);

      expect(await requestStatus(viewerPage, `/api/pages/${directDraft.id}/favorite`, 'PUT')).toBe(
        404,
      );
      expect(
        await requestStatus(viewerPage, `/api/pages/${inheritedDraft.id}/favorite`, 'PUT'),
      ).toBe(404);

      const publishedRow = viewerPage.getByTestId(`tree-node-${published.id}`);
      await publishedRow.hover();
      const favoriteResponse = viewerPage.waitForResponse(
        (response) =>
          response.request().method() === 'PUT' &&
          response.url().includes(`/api/pages/${published.id}/favorite`),
      );
      await publishedRow.getByTestId(`favorite-toggle-${published.id}`).click();
      expect((await favoriteResponse).ok()).toBe(true);
      await expect(
        viewerPage.getByTestId('favorites-section').getByText(published.title, { exact: true }),
      ).toBeVisible();

      await setDraft(page, await getPageByPath(page, published.slug), true);
      await viewerPage.reload();

      await expect.poll(() => getFavoritePageIds(viewerPage)).not.toContain(published.id);
      await expect(viewerPage.getByTestId('favorites-section')).toHaveCount(0);
      await expect(viewerPage.getByTestId(`tree-node-${published.id}`)).toHaveCount(0);
    } finally {
      await viewerContext.close();
    }
  });

  test('dragging an inherited draft to the public root keeps it private after restart', async ({
    page,
    browser,
  }) => {
    const containerName = process.env.E2E_CONTAINER_NAME;
    test.skip(
      !containerName,
      'A real restart requires the default Docker E2E harness; local mode has no container.',
    );

    await login(page);
    const stamp = Date.now();
    const source = await createNode(page, {
      title: `Drag draft source ${stamp}`,
      slug: `drag-draft-source-${stamp}`,
      kind: 'section',
      draft: true,
    });
    const child = await createNode(page, {
      title: `Dragged inherited child ${stamp}`,
      slug: `dragged-inherited-child-${stamp}`,
      parentId: source.id,
    });
    const publicTarget = await createNode(page, {
      title: `Public root target ${stamp}`,
      slug: `public-root-target-${stamp}`,
    });

    const originalChildPath = `${source.slug}/${child.slug}`;
    await page.goto(toAppPath(`/${originalChildPath}`));
    await expect(page.locator('article>h1')).toHaveText(child.title);
    await ensureSidebarOpen(page);
    await expect(page.getByTestId(`tree-node-${child.id}`)).toBeVisible();
    await dragNodeBefore(page, child.id, publicTarget.id);

    await expect.poll(() => new URL(page.url()).pathname).toBe(toAppPath(`/${child.slug}`));
    await expect(page.locator('article>h1')).toHaveText(child.title);
    await expect
      .poll(async () => {
        const moved = await getPageByPath(page, child.slug);
        return `${Boolean(moved.draft)}:${Boolean(moved.effectiveDraft)}:${moved.path}`;
      })
      .toBe(`true:true:${child.slug}`);
    await expect(page.getByTestId(`tree-node-${child.id}`).getByText('Draft')).toBeVisible();

    await execFileAsync('docker', ['restart', containerName!]);
    await expect
      .poll(
        async () => {
          try {
            const response = await page.request.get(toAppPath('/api/health'));
            return response.status();
          } catch {
            return 0;
          }
        },
        { timeout: 60_000 },
      )
      .toBe(200);

    expectDraftState(await getPageByPath(page, child.slug), true, true);
    const anonymous = await newAnonymousContext(browser);
    try {
      const response = await anonymous.request.get(
        toAppPath(`/api/pages/by-path?path=${encodeURIComponent(child.slug)}`),
      );
      expect(response.status()).toBe(404);

      const anonymousPage = await anonymous.newPage();
      await anonymousPage.goto(toAppPath(`/${child.slug}`));
      await new NotFoundPage(anonymousPage).expectVisible();
    } finally {
      await anonymous.close();
    }
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
