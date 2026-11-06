## ADDED Requirements

### Requirement: ccl serve records its log to a local file

`ccl serve` MUST write its log to a local file, because it is started without a terminal and a log that is discarded cannot be inspected when something goes wrong.

The log file MUST be written next to the other state of the launcher, MUST be readable only by the user, and MUST be kept from growing without bound.

Lines that report the quota refresh MUST carry a marker that consists only of ASCII characters, so that they can be selected even when the surrounding text cannot be displayed correctly.

#### Scenario: the log is written where it can be found
- **WHEN** the launcher starts the daemon on its own
- **THEN** the daemon writes its log to a file in the launcher's state directory

#### Scenario: quota lines can be selected without reading the text
- **WHEN** the daemon reports the outcome of a quota refresh
- **THEN** that line carries an ASCII marker that identifies it as a quota line
