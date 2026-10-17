"""Keep stateful Android journeys and their required screenshots in one shard."""
import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]


def selected_source(shard):
    selected = []
    excluded = False
    for line in (REPO / 'tests/e2e/android_client_test.go').read_text().splitlines():
        guard = re.fullmatch(r'\tif shard != "(network|files)" \{', line)
        if guard:
            excluded = shard == guard[1]
        elif line == '\t}':
            excluded = False
        elif not excluded:
            selected.append(line)
    return '\n'.join(selected)


class AndroidShardTests(unittest.TestCase):
    def phases(self, shard):
        return re.findall(r'(?:phase|phaseWhile)\("(\w+)"', selected_source(shard))

    def test_complete_phase_coverage_and_stateful_sequences(self):
        complete = self.phases('')
        network, files = self.phases('network'), self.phases('files')
        self.assertEqual(len(complete), len(set(complete)), 'Complete journey repeats a phase')
        self.assertEqual(set(network) | set(files), set(complete))
        common = ['enrollment', 'connectRememberedServer', 'foregroundRefresh',
                  'confirmationTouchThrough', 'pickerCancellation', 'changedServerKey',
                  'changedEndpoint', 'sendToThisDevice', 'exchange']
        self.assertEqual(network[:len(common)], common)
        self.assertEqual(files[:len(common)], common)
        self.assertEqual(set(network) & set(files), set(common))
        self.assertEqual(network[len(common):], ['discoveryTimeout', 'stalledRequest',
                         'retryAfterStall', 'receivedClipboardCases', 'multipleRecipients'])
        self.assertEqual(files[len(common):], ['removalFailure', 'restartAndRemove',
                         'inboxDeletion', 'fileTransfers'])
        for source in (selected_source('network'), selected_source('files')):
            self.assertIn('Expected one Android reply', source)
            self.assertIn('equalBytes(t, text,', source)
        self.assertIn('Expected binary and empty Android file replies', selected_source('files'))
        self.assertIn('removal-retry.bin', selected_source('files'))
        self.assertIn('Multi-recipient retry duplicated or omitted', selected_source('network'))

    def test_required_screenshots_have_a_producing_phase_in_each_shard(self):
        kotlin = (REPO / 'android/client/app/src/androidTest/java/dev/keyparcel/app/ClientJourneyTest.kt').read_text()
        methods = re.split(r'@Test fun (\w+)\(\)', kotlin)
        bodies = dict(zip(methods[1::2], methods[2::2]))
        required = {}
        for shard in ('', 'network', 'files'):
            source = selected_source(shard).split('// Every screenshot remains required', 1)[1]
            required[shard] = set(re.findall(r'"([\w-]+)"', source.split('for _, name := range screenshots')[0]))
            produced = {name for phase in self.phases(shard)
                        for kind in ('text', 'file')
                        for name in re.findall(r'screenshot\("([\w-]+)"', bodies[phase].replace('$kind', kind))}
            self.assertTrue(required[shard] <= produced,
                            f'{shard}: screenshots without producing phase: {required[shard] - produced}')
        self.assertEqual(required['network'] | required['files'], required[''])
