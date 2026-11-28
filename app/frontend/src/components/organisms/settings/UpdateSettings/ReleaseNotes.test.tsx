// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ReleaseNotes } from './ReleaseNotes';

const body = [
  '## SoundTap Core 0.1.0-alpha.7',
  '',
  '**This is an alpha**, a prerelease.',
  '',
  '### Install',
  '',
  '1. Download **SoundTap-Core.stu** below.',
  '',
  '### What changed',
  '',
  '**Changes**',
  '',
  '- feat(st20): the network chip joins open networks',
  '- fix: Bluetooth titles with `control` characters, see [the guide](https://example.org/guide)',
].join('\n');

describe('ReleaseNotes', () => {
  it('shows only what changed, as headings and a list without Markdown', () => {
    const { container } = render(<ReleaseNotes id="n" md={body} />);
    expect(screen.getByRole('heading', { name: 'Changes' })).toBeInTheDocument();
    const items = screen.getAllByRole('listitem').map((li) => li.textContent);
    expect(items).toEqual([
      'The network chip joins open networks',
      'Bluetooth titles with control characters, see the guide',
    ]);
    expect(container.textContent).not.toMatch(/\*\*|##|`|\]\(|Download|alpha/u);
    expect(container.querySelector('code')?.textContent).toBe('control');
  });

  it('keeps notes without a What changed part whole', () => {
    render(<ReleaseNotes id="n" md={'Kernel fixes\nand more'} />);
    expect(screen.getByText('Kernel fixes and more')).toBeInTheDocument();
  });
});
