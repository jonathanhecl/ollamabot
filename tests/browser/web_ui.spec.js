const { test, expect } = require('@playwright/test');
const path = require('path');
const fs = require('fs');

async function login(page, password) {
  console.log('Starting login flow...');
  await page.goto('/');
  await page.fill('#loginPassword', password);
  console.log('Submitting login form...');
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'load' }),
    page.click('#loginForm button[type="submit"]')
  ]);
  console.log('Login navigation complete. Current URL:', page.url());
  // Wait for the active model badge to exist in DOM, ensuring models are loaded
  await page.waitForSelector('#capabilityBadges .model-badge', { state: 'attached' });
  console.log('Model badge found! Login flow successful.');
}

test.describe('OllamaBot Web UI Integration Tests', () => {

  test.beforeEach(({ page }) => {
    // Pipe page console logs and errors to the test runner process
    page.on('console', msg => {
      console.log(`[PAGE LOG] [${msg.type()}] ${msg.text()}`);
    });
    page.on('pageerror', err => {
      console.error(`[PAGE UNHANDLED ERROR] ${err.stack || err.message}`);
    });
    page.on('request', req => {
      console.log(`[PAGE REQ] ${req.method()} ${req.url()}`);
    });
    page.on('response', res => {
      console.log(`[PAGE RES] ${res.status()} ${res.url()}`);
    });
  });

  test('should handle login and show/hide overlay correctly', async ({ page }) => {
    // 1. Load page
    await page.goto('/');

    // 2. Verify login overlay is visible
    const overlay = page.locator('#loginOverlay');
    await expect(overlay).toBeVisible();

    // 3. Try invalid password
    await page.fill('#loginPassword', 'wrongpass');
    await page.click('#loginForm button[type="submit"]');

    // Verify error is shown
    const errorDiv = page.locator('#loginError');
    await expect(errorDiv).toBeVisible();
    await expect(errorDiv).toContainText('Incorrect password');

    // 4. Enter correct password
    await page.fill('#loginPassword', 'testpass');
    await Promise.all([
      page.waitForNavigation({ waitUntil: 'load' }),
      page.click('#loginForm button[type="submit"]')
    ]);

    // Verify overlay closes
    await expect(overlay).toBeHidden();
  });

  test('should handle drag & drop of media files', async ({ page }) => {
    // Login first and wait for ready state
    await login(page, 'testpass');

    // Prepare drag & drop simulation
    const filePath = path.join(__dirname, 'assets', 'test_image.png');
    const fileName = path.basename(filePath);
    const mimeType = 'image/png';
    const base64 = fs.readFileSync(filePath).toString('base64');

    // Dispatch custom dragover and drop events in browser context
    await page.evaluate(async ({ base64, mimeType, fileName }) => {
      const response = await fetch(`data:${mimeType};base64,${base64}`);
      const blob = await response.blob();
      const file = new File([blob], fileName, { type: mimeType });

      const mockDataTransfer = {
        files: [file],
        items: [
          {
            kind: 'file',
            type: mimeType,
            getAsFile: () => file
          }
        ]
      };

      const dropZone = document.querySelector('.app');
      
      const dragOverEvent = new Event('dragover', { bubbles: true, cancelable: true });
      Object.defineProperty(dragOverEvent, 'dataTransfer', { value: mockDataTransfer });
      dropZone.dispatchEvent(dragOverEvent);

      const dropEvent = new Event('drop', { bubbles: true, cancelable: true });
      Object.defineProperty(dropEvent, 'dataTransfer', { value: mockDataTransfer });
      dropZone.dispatchEvent(dropEvent);
    }, { base64, mimeType, fileName });

    // Verify that the file appears in the attachment preview list
    const attachmentItem = page.locator('#attachments .attachment');
    await expect(attachmentItem).toBeVisible();
    await expect(attachmentItem).toContainText(fileName);
  });

  test('should support copying message content and code blocks', async ({ page, context }) => {
    // Grant clipboard access
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);

    // Login and wait for ready state
    await login(page, 'testpass');

    // Send a message to get a response
    await page.fill('#prompt', 'Hello test');
    await page.click('#sendBtn');

    // Wait for assistant response bubble to appear
    const lastMsg = page.locator('#messages .message.assistant').last();
    await expect(lastMsg).toBeVisible();

    // Hover over the message bubble to make the copy button appear
    await lastMsg.hover();

    // Verify message copy button exists
    const msgCopyBtn = lastMsg.locator('.message-copy-btn');
    await expect(msgCopyBtn).toBeVisible();

    // Click message copy button
    await msgCopyBtn.click();

    // Verify clipboard content matches the message markdown
    let clipboardText = await page.evaluate(() => navigator.clipboard.readText());
    expect(clipboardText).toContain("Here is some code");

    // Verify code block copy button exists
    const codeBlockWrapper = lastMsg.locator('.code-block-wrapper').first();
    await expect(codeBlockWrapper).toBeVisible();

    const codeCopyBtn = codeBlockWrapper.locator('.code-block-copy-btn');
    await expect(codeCopyBtn).toBeVisible();

    // Click code block copy button
    await codeCopyBtn.click();

    // Verify clipboard content matches the code block
    clipboardText = await page.evaluate(() => navigator.clipboard.readText());
    expect(clipboardText).toContain("console.log('hello')");
  });
});
