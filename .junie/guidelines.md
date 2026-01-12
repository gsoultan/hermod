### Junie Guidelines - Hermod Project

#### General Principles
- **Keep it Simple**: Favor clear, readable code over clever or complex solutions.
- **Consistency**: Follow existing patterns and naming conventions in the project.
- **Small Commits**: Aim for small, logical changes that are easy to review.

#### Go Specific Guidelines
- **Standard Formatting**: Always use `gofmt` or `goimports` to format Go code.
- **Error Handling**: Explicitly handle all errors. Do not ignore them unless there's a very good reason.
- **Testing**:
    - Write unit tests for new logic.
    - Use the standard `testing` package.
    - Table-driven tests are preferred for multiple test cases.
- **Documentation**: Use standard Go doc comments for exported symbols.

#### Workflow
- Always check for existing tests before making changes.
- Ensure the project compiles after every change.
- Update the README if any significant features are added or changed.
